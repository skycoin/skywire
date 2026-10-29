package com.skycoin.skywire.core

import com.skycoin.skywire.api.BookContact
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import java.io.File

/** A contact of the visor's book as SkyMail sees it: its name and the mailboxes it reaches. */
data class MailContact(val uid: String, val name: String, val addresses: List<String>) {
    /** The address to write to, and the one an edit changes. */
    val address: String get() = addresses.first()
}

/**
 * SkyMail's side of the visor's contact book, which SkyChat and the dashboard
 * share. A contact reaches each Skymail address in its mail and the mailbox
 * of each of its keys, one entry per key whatever the local part or network.
 */
object MailContacts {

    /** The book's contacts that mail can reach, in the book's name order. */
    fun of(book: List<BookContact>): List<MailContact> = book.mapNotNull { c ->
        val addresses = (c.mail.filter { pkOf(it) != null } + c.keys.mapNotNull { SkymailAddress.of(it) })
            .distinctBy(::pkOf)
        if (addresses.isEmpty()) null else MailContact(c.uid, c.name, addresses)
    }

    /**
     * The contact to store when [name] is saved for [address], a Skymail
     * address. Editing [uid] moves it off the mailbox of [replacing]; a new
     * name for a mailbox already in the book renames the contact holding it.
     * Whatever else the contact holds is kept.
     */
    fun saved(
        book: List<BookContact>,
        name: String,
        address: String,
        uid: String? = null,
        replacing: String? = null,
    ): BookContact {
        val pk = requireNotNull(pkOf(address)) { "not a Skymail address: $address" }
        val old = uid?.let { id -> book.firstOrNull { it.uid == id } } ?: book.firstOrNull { pk in keysOf(it) }
        val moved = replacing?.let(::pkOf)
        val base = old ?: BookContact()
        return base.copy(
            name = name.trim(),
            mail = listOf(address) + base.mail.filterNot { pkOf(it).let { k -> k == pk || k == moved } },
            keys = (base.keys.map { if (it.lowercase() == moved) pk else it } + pk).distinctBy { it.lowercase() },
        )
    }

    /** The mailboxes of [contact]: the keys of its Skymail addresses and its own keys. */
    private fun keysOf(contact: BookContact): Set<String> =
        (contact.mail.mapNotNull(::pkOf) + contact.keys.map { it.lowercase() }).toSet()

    private fun pkOf(address: String): String? = SkymailAddress.pkOfAddress(address)?.lowercase()

    // --- the app's own book, before the visor had one ---

    @Serializable
    private data class Legacy(val name: String, val address: String)

    private val json = Json { ignoreUnknownKeys = true }

    /**
     * The contacts to add to [book] from [file], the JSON list SkyMail kept
     * before the visor had a book. Mailboxes the book holds already are
     * skipped, so importing twice adds nothing.
     */
    fun imports(file: File, book: List<BookContact>): List<BookContact> {
        val legacy = runCatching {
            json.decodeFromString(ListSerializer(Legacy.serializer()), file.readText())
        }.getOrDefault(emptyList())
        val held = book.flatMap(::keysOf).toMutableSet()
        return legacy.mapNotNull { c ->
            val pk = pkOf(c.address)
            if (pk == null || c.name.isBlank() || !held.add(pk)) return@mapNotNull null
            BookContact(name = c.name.trim(), keys = listOf(pk), mail = listOf(c.address.trim()))
        }
    }
}
