package com.skycoin.skywire.chat

import android.app.DownloadManager
import android.content.Context
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.util.Base64
import androidx.core.net.toUri
import com.skycoin.skywire.R
import com.skycoin.skywire.core.VoiceCalls
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.time.LocalDate

// --- conversations ---

/** Delete a DM (or empty the notes): local copy, the stored history and a live pair all go. */
fun ChatRepository.deleteChat(key: String) {
    if (key == SAVED_KEY) {
        mutate(SAVED_KEY) { emptyList() }
        note(R.string.chat_note_saved_cleared)
        return
    }
    val pk = key
    scope.launch { runCatching { api.accept(pk, accepted = false) } }
    if (pk in pair.value.paired) {
        pair.update { it.copy(paired = it.paired - pk) }
        scope.launch {
            runCatching { api.unpair(pk) }.onFailure { e ->
                if (e !is SkychatHttpException || (e.code != 404 && e.code != 503)) note(R.string.chat_note_pair_not_revoked, error = true)
            }
        }
    }
    scope.launch {
        runCatching { api.forget(pk) }.onFailure { e ->
            if (e !is SkychatHttpException || (e.code != 404 && e.code != 503)) note(R.string.chat_note_history_not_deleted, error = true)
        }
    }
    unpinIf("dm:$pk")
    store.update { s -> s.copy(recipients = s.recipients - pk, readUpTo = s.readUpTo - pk) }
    threads.remove(pk)
    store.deleteThread(pk)
    summaries.update { it - pk }
    note(R.string.chat_note_chat_deleted)
}

fun pinKey(key: String, group: Boolean): String = if (group) "grp:$key" else "dm:$key"

fun ChatRepository.togglePin(key: String) {
    val pin = pinKey(key, isGroupKey(key))
    store.update { s -> s.copy(pinned = if (s.pinned == pin) null else pin) }
}

fun ChatRepository.isMuted(key: String): Boolean = key.lowercase() in store.notif.value.muted || key in store.notif.value.muted

fun ChatRepository.toggleMute(key: String) {
    val muted = isMuted(key)
    store.updateNotif { n -> n.copy(muted = if (muted) n.muted - key - key.lowercase() else n.muted + key) }
    note(if (muted) R.string.chat_note_unmuted else R.string.chat_note_muted)
}

fun ChatRepository.setTab(tab: String) {
    store.update { it.copy(tab = tab) }
}

// --- privacy ---

fun ChatRepository.acceptRequest(pk: String) {
    scope.launch {
        runCatching { api.accept(pk) }
            .onSuccess {
                applyPrivacy(it)
                addContact(pk)
                note(R.string.chat_note_can_message, displayName(pk))
            }
            .onFailure { fail(R.string.chat_note_accept_failed, it) }
    }
}

/** From the request bar: block and drop the conversation. */
fun ChatRepository.blockRequest(pk: String, done: () -> Unit) {
    scope.launch {
        runCatching { api.block(pk, true) }
            .onSuccess {
                applyPrivacy(it)
                val name = displayName(pk)
                deleteChat(pk)
                note(R.string.chat_note_blocked, name)
                done()
            }
            .onFailure { fail(R.string.chat_note_block_failed, it) }
    }
}

fun ChatRepository.setBlocked(pk: String, blocked: Boolean) {
    scope.launch {
        runCatching { api.block(pk, blocked) }
            .onSuccess {
                applyPrivacy(it)
                note(if (blocked) R.string.chat_note_blocked_keep else R.string.chat_note_unblocked, displayName(pk))
            }
            .onFailure { fail(if (blocked) R.string.chat_note_block_failed else R.string.chat_note_unblock_failed, it) }
    }
}

fun ChatRepository.setApproval(on: Boolean) {
    scope.launch {
        runCatching { api.setApproval(on) }
            .onSuccess { applyPrivacy(it) }
            .onFailure {
                fail(R.string.chat_note_setting_failed, it)
                syncPrivacy()
            }
    }
}

// --- pairing ---

fun ChatRepository.togglePair(pk: String) {
    if (!pair.value.available) return
    scope.launch {
        if (pk in pair.value.paired) {
            runCatching { api.unpair(pk) }
                .onSuccess {
                    pair.update { it.copy(paired = it.paired - pk) }
                    if (network.value == "cxo") network.value = "skynet"
                    note(R.string.chat_note_pair_removed)
                }
                .onFailure { fail(R.string.chat_note_unpair_failed, it) }
        } else {
            runCatching { api.pair(pk) }
                .onSuccess {
                    pair.update { it.copy(paired = it.paired + pk) }
                    note(R.string.chat_note_pair_sent)
                }
                .onFailure { fail(R.string.chat_note_pair_failed, it) }
        }
    }
}

fun ChatRepository.acceptInvite(pk: String) {
    scope.launch {
        runCatching { api.acceptInvite(pk) }
            .onSuccess {
                pair.update { it.copy(invites = it.invites - pk, paired = it.paired + pk) }
                addContact(pk)
                ensureRecipient(pk)
                note(R.string.chat_note_invite_accepted)
            }
            .onFailure { fail(R.string.chat_note_accept_failed, it) }
    }
}

fun ChatRepository.declineInvite(pk: String) {
    scope.launch {
        runCatching { api.declineInvite(pk) }
            .onSuccess {
                pair.update { it.copy(invites = it.invites - pk) }
                note(R.string.chat_note_invite_declined)
            }
            .onFailure { fail(R.string.chat_note_decline_failed, it) }
    }
}

// --- contacts ---

fun ChatRepository.removeContact(pk: String) {
    store.update { s -> s.copy(contacts = s.contacts - pk) }
    setName(pk, "")
    scope.launch { runCatching { api.accept(pk, accepted = false) } }
    note(R.string.chat_note_contact_removed)
}

/** A peer's published profile, or null when it cannot be had. `unreachable` is told apart. */
data class PeerLookup(val name: String = "", val avatar: ByteArray? = null, val unreachable: Boolean = false)

suspend fun ChatRepository.lookupPeer(pk: String): PeerLookup? {
    if (profileAvailable.value == false) return null
    return try {
        val p = api.peerProfile(pk)
        PeerLookup(p.name, decodeDataUrl(p.avatar))
    } catch (e: Exception) {
        PeerLookup(unreachable = true)
    }
}

fun decodeDataUrl(uri: String): ByteArray? {
    val b64 = uri.substringAfter("base64,", "").ifEmpty { return null }
    return runCatching { Base64.decode(b64, Base64.DEFAULT) }.getOrNull()
}

fun dataUrl(bytes: ByteArray): String {
    val png = bytes.size > 4 && bytes[1] == 'P'.code.toByte() && bytes[2] == 'N'.code.toByte()
    return "data:image/" + (if (png) "png" else "jpeg") + ";base64," + Base64.encodeToString(bytes, Base64.NO_WRAP)
}

// --- own profile ---

/** Publish name (and picture, when [avatar] is given); the visor clears a picture a POST leaves out, so the current one is resent. */
fun ChatRepository.saveProfile(name: String, avatar: ByteArray?, done: (Boolean) -> Unit) {
    val keep = avatar?.let { dataUrl(it) } ?: profile.value?.avatar?.takeIf { it.isNotEmpty() }
    scope.launch {
        runCatching { api.setProfile(name.trim(), keep) }
            .onSuccess {
                profile.value = it
                note(R.string.chat_note_profile_published)
                done(true)
            }
            .onFailure {
                fail(R.string.chat_note_failed, it)
                done(false)
            }
    }
}

fun ChatRepository.removeProfilePicture(done: (Boolean) -> Unit) {
    val name = profile.value?.name.orEmpty()
    scope.launch {
        runCatching { api.setProfile(name, null) }
            .onSuccess {
                profile.value = it
                note(R.string.chat_note_picture_removed)
                done(true)
            }
            .onFailure {
                fail(R.string.chat_note_failed, it)
                done(false)
            }
    }
}

fun ChatRepository.clearProfile() {
    scope.launch {
        runCatching { api.clearProfile() }
            .onSuccess {
                profile.value = it
                note(R.string.chat_note_profile_cleared)
            }
            .onFailure { fail(R.string.chat_note_failed, it) }
    }
}

fun ChatRepository.myAddress(): String =
    profile.value?.address?.takeIf { it.isNotEmpty() } ?: myPk.value.takeIf { it.isNotEmpty() }?.let { "skychat://$it" }.orEmpty()

// --- calls ---

fun ChatRepository.placeCall(pk: String) {
    val calls = VoiceCalls.state.value
    if (calls.inCall) {
        note(R.string.chat_note_in_call, error = true)
        return
    }
    if (calls.outgoing?.state?.ended == false) {
        note(R.string.chat_note_already_calling, error = true)
        return
    }
    scope.launch {
        runCatching { http.post("/voice/call", "{\"peer\":\"$pk\"}", slowPeer = true) }
            .onSuccess { VoiceCalls.refresh() }
            .onFailure { fail(R.string.chat_note_call_failed, it) }
    }
}

suspend fun ChatRepository.callLog(): List<CallRecord>? = runCatching { api.calls() }.getOrNull()

// --- archive ---

/** The system download manager fetches `/export/<name>` with our credential. */
fun ChatRepository.exportArchive() {
    val name = "skychat-${LocalDate.now()}.json"
    scope.launch {
        runCatching {
            val request = DownloadManager.Request(http.url("/export/$name").toUri())
                .setTitle(name)
                .addRequestHeader("Authorization", http.authorization())
                .setMimeType("application/json")
                .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                request.setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, name)
            } else {
                request.setDestinationInExternalFilesDir(app, Environment.DIRECTORY_DOWNLOADS, name)
            }
            (app.getSystemService(Context.DOWNLOAD_SERVICE) as DownloadManager).enqueue(request)
        }.onSuccess { note(R.string.chat_note_archive_requested) }.onFailure { fail(R.string.chat_note_failed, it) }
    }
}

/** Import an archive; returns the summary lines to show. */
suspend fun ChatRepository.importArchive(uri: Uri): String {
    val bytes = withContext(Dispatchers.IO) { runCatching { app.contentResolver.openInputStream(uri)?.use { it.readBytes() } }.getOrNull() }
        ?: return app.getString(R.string.chat_import_unreadable)
    val (ok, body) = try {
        true to api.importArchive(bytes)
    } catch (e: SkychatHttpException) {
        false to (e.message ?: "")
    } catch (e: Exception) {
        return app.getString(R.string.chat_import_failed, errorText(e))
    }
    val report = runCatching { SkychatClient.json.decodeFromString(ImportReport.serializer(), body) }.getOrNull()
        ?: return if (ok) app.getString(R.string.chat_import_done) else body.trim().ifEmpty { app.getString(R.string.chat_import_failed, "") }
    syncNames()
    catchUp()
    return importSummary(report)
}

private fun ChatRepository.importSummary(r: ImportReport): String {
    fun count(n: Int, one: Int, many: Int) = app.getString(if (n == 1) one else many, n)
    val lines = mutableListOf<String>()
    if (r.error.isNotEmpty()) lines += app.getString(R.string.chat_import_stopped, r.error)
    val items = listOfNotNull(
        r.messages.takeIf { it > 0 }?.let { count(it, R.string.chat_import_message_one, R.string.chat_import_message_many) },
        r.groupMessages.takeIf { it > 0 }?.let { count(it, R.string.chat_import_group_one, R.string.chat_import_group_many) },
        r.contacts.takeIf { it > 0 }?.let { count(it, R.string.chat_import_name_one, R.string.chat_import_name_many) },
    )
    if (items.isNotEmpty()) lines += app.getString(R.string.chat_import_items, items.joinToString(", "))
    if (r.duplicates > 0) lines += app.getString(R.string.chat_import_duplicates, r.duplicates)
    if (r.persistence == false) lines += app.getString(R.string.chat_import_no_persist)
    if (r.evicted > 0) lines += app.getString(R.string.chat_import_evicted, r.evicted)
    if (r.expiring > 0) lines += app.getString(R.string.chat_import_expiring, r.expiring)
    if (r.rejected > 0) lines += app.getString(R.string.chat_import_rejected, r.rejected)
    if (items.isEmpty()) lines += app.getString(R.string.chat_import_nothing)
    return lines.joinToString("\n")
}

// --- ringback ---

suspend fun ChatRepository.ringbackInfo(): RingbackInfo? = runCatching { api.ringbackInfo() }.getOrNull()

suspend fun ChatRepository.setRingback(uri: Uri): String? {
    val resolver = app.contentResolver
    val bytes = withContext(Dispatchers.IO) { runCatching { resolver.openInputStream(uri)?.use { it.readBytes() } }.getOrNull() }
        ?: return app.getString(R.string.chat_import_unreadable)
    if (bytes.size > RINGBACK_MAX) return app.getString(R.string.chat_ringback_too_big, humanSize(bytes.size.toLong()))
    val type = resolver.getType(uri)?.takeIf { it.startsWith("audio/") } ?: run {
        val name = uri.lastPathSegment.orEmpty().lowercase()
        when (name.substringAfterLast('.', "")) {
            "mp3" -> "audio/mpeg"
            "ogg", "oga", "opus" -> "audio/ogg"
            "weba", "webm" -> "audio/webm"
            "wav" -> "audio/wav"
            "m4a" -> "audio/mp4"
            "aac" -> "audio/aac"
            "flac" -> "audio/flac"
            else -> return app.getString(R.string.chat_ringback_bad_type)
        }
    }
    return runCatching { api.setRingback(bytes, type) }.fold({ null }, { errorText(it) })
}

suspend fun ChatRepository.clearRingback(): String? = runCatching { api.clearRingback() }.fold({ null }, { errorText(it) })

private const val RINGBACK_MAX = 1024 * 1024

// --- search ---

data class SearchResult(val key: String, val isGroup: Boolean, val from: String, val text: String, val ts: Long)

/** Server hits first, then the local threads; the same message within two minutes counts once. */
suspend fun ChatRepository.searchMessages(q: String): List<SearchResult> {
    val needle = q.trim().lowercase()
    if (needle.isEmpty()) return emptyList()
    val server = runCatching { api.search(q.trim()) }.getOrDefault(emptyList()).mapNotNull { h ->
        val ts = parseTime(h.ts) ?: return@mapNotNull null
        val key = h.groupId.ifEmpty { h.peer.lowercase() }
        SearchResult(key, h.groupId.isNotEmpty(), if (h.outgoing) ChatMessage.ME else h.from.ifEmpty { h.peer }.lowercase(), h.text, ts)
    }
    val local = threads.flatMap { (key, flow) ->
        flow.value.filter { it.text.lowercase().contains(needle) }.map { SearchResult(key, isGroupKey(key), it.from, it.text, it.ts) }
    }
    val out = mutableListOf<SearchResult>()
    for (h in server + local) {
        if (out.none { it.key == h.key && it.text == h.text && kotlin.math.abs(it.ts - h.ts) < 120_000 }) out += h
    }
    return out.sortedByDescending { it.ts }.take(100)
}
