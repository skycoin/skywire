package com.skycoin.skywire.core

import android.Manifest
import android.accounts.Account
import android.accounts.AccountManager
import android.content.ContentProviderOperation
import android.content.ContentResolver
import android.content.ContentUris
import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Bundle
import android.provider.ContactsContract
import android.provider.ContactsContract.CommonDataKinds.Email
import android.provider.ContactsContract.CommonDataKinds.Note
import android.provider.ContactsContract.CommonDataKinds.StructuredName
import android.provider.ContactsContract.Data
import android.provider.ContactsContract.RawContacts
import android.util.Log
import androidx.core.content.ContextCompat
import com.skycoin.skywire.R
import com.skycoin.skywire.api.BookContact
import com.skycoin.skywire.api.VisorApi
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.security.MessageDigest

/**
 * The visor's contact book, copied into the phone's contacts under a Skywire
 * account so the Contacts app shows the names. Off until asked for: any app
 * allowed to read contacts can then read the book, which otherwise never
 * leaves the visor (pkg/contacts).
 *
 * The book stays the one source. The Contacts app may not edit the account
 * (res/xml/syncadapter.xml), and each sync makes the copy match the book.
 */
object PhoneContacts {

    const val PREF_KEY = "phone_contacts"

    /** A skywire key. Its action in the Contacts app opens a chat (res/xml/contacts.xml). */
    const val MIME_CHAT = "vnd.android.cursor.item/vnd.com.skycoin.skywire.chat"

    /** A Skymail address. Its action opens a draft to it. */
    const val MIME_MAIL = "vnd.android.cursor.item/vnd.com.skycoin.skywire.mail"

    val PERMISSIONS = arrayOf(Manifest.permission.READ_CONTACTS, Manifest.permission.WRITE_CONTACTS)

    private const val TAG = "PhoneContacts"
    private const val ACCOUNT_NAME = "Skywire"
    private const val SYNC_PERIOD_S = 3_600L

    /** The provider refuses a batch of more than 500 operations. */
    private const val BATCH_OPS = 400

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val lock = Mutex()

    fun account(context: Context) = Account(ACCOUNT_NAME, context.getString(R.string.phone_contacts_account_type))

    fun granted(context: Context): Boolean = PERMISSIONS.all {
        ContextCompat.checkSelfPermission(context, it) == PackageManager.PERMISSION_GRANTED
    }

    suspend fun enabled(context: Context): Boolean =
        AppPreferences(context).boolean(PREF_KEY).first() && granted(context)

    /** Turns the copy on, once the permissions are granted, and fills it. */
    suspend fun enable(context: Context) {
        AppPreferences(context).putBoolean(PREF_KEY, true)
        refresh(context)
    }

    suspend fun disable(context: Context) {
        AppPreferences(context).putBoolean(PREF_KEY, false)
        forget(context)
    }

    /**
     * Removes the account, and with it every contact the provider holds for
     * it. The choice stays, so a new identity's book fills a new account.
     */
    fun forget(context: Context) {
        val manager = AccountManager.get(context)
        manager.getAccountsByType(account(context).type).forEach { manager.removeAccountExplicitly(it) }
    }

    /** Brings the copy up to date in the background, when it is on. */
    fun refresh(context: Context) {
        val app = context.applicationContext
        scope.launch { runCatching { sync(app) }.onFailure { Log.w(TAG, "contacts copy not updated", it) } }
    }

    /**
     * Makes the copy match the book. A visor that does not answer leaves the
     * copy as it was; a revoked permission removes it, as it could not follow the book.
     */
    suspend fun sync(context: Context): Unit = lock.withLock {
        if (!enabled(context)) return@withLock forget(context)
        val book = VisorApi.get(context).contacts()
        write(context, ensureAccount(context), book)
    }

    private fun ensureAccount(context: Context): Account {
        val account = account(context)
        val manager = AccountManager.get(context)
        if (account in manager.getAccountsByType(account.type)) return account
        manager.addAccountExplicitly(account, null, null)
        ContentResolver.setIsSyncable(account, ContactsContract.AUTHORITY, 1)
        ContentResolver.setSyncAutomatically(account, ContactsContract.AUTHORITY, true)
        ContentResolver.addPeriodicSync(account, ContactsContract.AUTHORITY, Bundle.EMPTY, SYNC_PERIOD_S)
        // The provider hides an account's contacts that are in no group until told otherwise.
        val settings = asSyncAdapter(ContactsContract.Settings.CONTENT_URI, account)
        val visible = ContentValues().apply {
            put(ContactsContract.Settings.ACCOUNT_NAME, account.name)
            put(ContactsContract.Settings.ACCOUNT_TYPE, account.type)
            put(ContactsContract.Settings.UNGROUPED_VISIBLE, 1)
        }
        val where = "${ContactsContract.Settings.ACCOUNT_NAME}=? AND ${ContactsContract.Settings.ACCOUNT_TYPE}=?"
        if (context.contentResolver.update(settings, visible, where, arrayOf(account.name, account.type)) == 0) {
            context.contentResolver.insert(settings, visible)
        }
        return account
    }

    private fun write(context: Context, account: Account, book: List<BookContact>) {
        val resolver = context.contentResolver
        val rawUri = asSyncAdapter(RawContacts.CONTENT_URI, account)
        val dataUri = asSyncAdapter(Data.CONTENT_URI, account)
        // Book uid to the raw contact holding it and the stamp of what it holds.
        val held = HashMap<String, Pair<Long, String?>>()
        val stray = ArrayList<Long>()
        resolver.query(
            rawUri,
            arrayOf(RawContacts._ID, RawContacts.SOURCE_ID, RawContacts.SYNC1, RawContacts.DELETED),
            "${RawContacts.ACCOUNT_NAME}=? AND ${RawContacts.ACCOUNT_TYPE}=?",
            arrayOf(account.name, account.type),
            null,
        )?.use { c ->
            while (c.moveToNext()) {
                val uid = c.getString(1)
                val gone = c.getInt(3) != 0
                if (uid == null || gone || held.put(uid, c.getLong(0) to c.getString(2)) != null) stray += c.getLong(0)
            }
        }
        val ops = ArrayList<ContentProviderOperation>()
        val flush = {
            if (ops.isNotEmpty()) resolver.applyBatch(ContactsContract.AUTHORITY, ops)
            ops.clear()
        }
        val chat = { pk: String -> context.getString(R.string.phone_contacts_chat, VoiceCallWatcher.shortPk(pk)) }
        val mail = { address: String -> context.getString(R.string.phone_contacts_mail, SkymailAddress.short(address)) }
        for (contact in book) {
            val rows = rows(contact, chat, mail)
            val stamp = stamp(rows)
            val old = held.remove(contact.uid)
            if (old?.second == stamp) continue
            // A contact's operations go in one batch: the back reference counts from its start.
            if (ops.size >= BATCH_OPS) flush()
            val rawId = old?.first
            val first = ops.size
            if (rawId == null) {
                ops += ContentProviderOperation.newInsert(rawUri)
                    .withValue(RawContacts.ACCOUNT_NAME, account.name)
                    .withValue(RawContacts.ACCOUNT_TYPE, account.type)
                    .withValue(RawContacts.SOURCE_ID, contact.uid)
                    .withValue(RawContacts.SYNC1, stamp)
                    .build()
            } else {
                ops += ContentProviderOperation.newUpdate(asSyncAdapter(ContentUris.withAppendedId(RawContacts.CONTENT_URI, rawId), account))
                    .withValue(RawContacts.SYNC1, stamp)
                    .build()
                ops += ContentProviderOperation.newDelete(dataUri)
                    .withSelection("${Data.RAW_CONTACT_ID}=?", arrayOf(rawId.toString()))
                    .build()
            }
            rows.forEach { row ->
                val insert = ContentProviderOperation.newInsert(dataUri)
                if (rawId == null) insert.withValueBackReference(Data.RAW_CONTACT_ID, first) else insert.withValue(Data.RAW_CONTACT_ID, rawId)
                row.forEach { (k, v) -> insert.withValue(k, v) }
                ops += insert.build()
            }
        }
        (held.values.map { it.first } + stray).forEach { id ->
            if (ops.size >= BATCH_OPS) flush()
            ops += ContentProviderOperation.newDelete(asSyncAdapter(ContentUris.withAppendedId(RawContacts.CONTENT_URI, id), account)).build()
        }
        flush()
    }

    /**
     * The data rows for [contact], as column to value. Skymail addresses get
     * a row of their own rather than an e-mail one: a mail app cannot reach them.
     */
    internal fun rows(contact: BookContact, chat: (String) -> String, mail: (String) -> String): List<Map<String, Any>> = buildList {
        add(mapOf(Data.MIMETYPE to StructuredName.CONTENT_ITEM_TYPE, StructuredName.DISPLAY_NAME to contact.name))
        contact.keys.forEach { pk ->
            add(mapOf(Data.MIMETYPE to MIME_CHAT, Data.DATA1 to pk, Data.DATA2 to "SkyChat", Data.DATA3 to chat(pk)))
        }
        MailContacts.of(listOf(contact)).firstOrNull()?.addresses.orEmpty().forEach { address ->
            add(mapOf(Data.MIMETYPE to MIME_MAIL, Data.DATA1 to address, Data.DATA2 to "SkyMail", Data.DATA3 to mail(address)))
        }
        contact.mail.filter { SkymailAddress.pkOfAddress(it) == null }.forEach { address ->
            add(mapOf(Data.MIMETYPE to Email.CONTENT_ITEM_TYPE, Email.ADDRESS to address, Email.TYPE to Email.TYPE_OTHER))
        }
        if (contact.notes.isNotBlank()) add(mapOf(Data.MIMETYPE to Note.CONTENT_ITEM_TYPE, Note.NOTE to contact.notes))
    }

    /** What a raw contact holds, so a sync rewrites only the contacts that changed. */
    internal fun stamp(rows: List<Map<String, Any>>): String =
        MessageDigest.getInstance("SHA-256").digest(rows.toString().toByteArray())
            .joinToString("") { "%02x".format(it) }

    private fun asSyncAdapter(uri: Uri, account: Account): Uri = uri.buildUpon()
        .appendQueryParameter(ContactsContract.CALLER_IS_SYNCADAPTER, "true")
        .appendQueryParameter(RawContacts.ACCOUNT_NAME, account.name)
        .appendQueryParameter(RawContacts.ACCOUNT_TYPE, account.type)
        .build()

    /**
     * Acts on a tap on one of our rows in the Contacts app, which hands over
     * the row and its type. True when [intent] was one.
     */
    fun offer(context: Context, intent: Intent?): Boolean {
        val type = intent?.type
        if (intent?.action != Intent.ACTION_VIEW || (type != MIME_CHAT && type != MIME_MAIL)) return false
        val uri = intent.data?.takeIf { it.authority == ContactsContract.AUTHORITY } ?: return false
        if (!granted(context)) return false
        val value = runCatching {
            context.contentResolver.query(uri, arrayOf(Data.MIMETYPE, Data.DATA1), null, null, null)?.use { c ->
                if (c.moveToFirst() && c.getString(0) == type) c.getString(1) else null
            }
        }.getOrNull() ?: return false
        if (type == MIME_CHAT) return DeepLinks.offer(Uri.parse("${DeepLinks.SKYCHAT_SCHEME}://$value"))
        DeepLinks.composeMail(value)
        return true
    }
}
