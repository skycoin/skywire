package com.skycoin.skywire.chat

import android.net.Uri
import android.provider.OpenableColumns
import android.webkit.MimeTypeMap
import com.skycoin.skywire.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import java.io.File
import java.io.IOException
import java.util.UUID

/**
 * The peer link: dial first, send after (index.html _linkWatch). Only the
 * open DM is watched; messages typed before the route is up wait in order.
 */
internal object LinkWatch {

    private var job: Job? = null
    private var watched: String? = null

    fun watch(repo: ChatRepository, pk: String?) {
        if (pk == watched && job?.isActive == true) return
        job?.cancel()
        watched = pk
        if (pk == null || repo.linkOff || repo.network.value == "cxo") return
        job = repo.scope.launch {
            var state = start(repo, pk) ?: return@launch
            while (true) {
                delay(if (state.ready) 8_000 else 1_500)
                state = try {
                    repo.api.linkState(pk)
                } catch (e: SkychatHttpException) {
                    disable(repo)
                    return@launch
                } catch (_: IOException) {
                    delay(4_000)
                    continue
                }
                set(repo, pk, state)
                if (!state.ready && !state.connecting && state.error.isEmpty()) state = start(repo, pk) ?: return@launch
            }
        }
    }

    /** Retry from the link bar. */
    fun retry(repo: ChatRepository, pk: String) {
        set(repo, pk, LinkState(connecting = true))
        watched = null
        watch(repo, pk)
    }

    private suspend fun start(repo: ChatRepository, pk: String): LinkState? = try {
        repo.api.linkStart(pk, repo.network.value).also { set(repo, pk, it) }
    } catch (e: SkychatHttpException) {
        disable(repo)
        null
    } catch (_: IOException) {
        LinkState(error = "")
    }

    private fun set(repo: ChatRepository, pk: String, state: LinkState) {
        val was = repo.links.value[pk]?.ready == true
        repo.links.update { it + (pk to state) }
        if (state.ready && !was) repo.flushOutbox(pk)
    }

    /** No `/link` on this skychat: send straight away from now on. */
    private fun disable(repo: ChatRepository) {
        repo.linkOff = true
        repo.outbox.keys.toList().forEach { repo.flushOutbox(it) }
    }

    /** Wait for [pk]'s link, up to [timeoutMs]; true when it is ready (or links are off). */
    suspend fun await(repo: ChatRepository, pk: String, timeoutMs: Long): Boolean {
        if (repo.linkOff || repo.links.value[pk]?.ready == true) return true
        runCatching { repo.api.linkStart(pk, repo.network.value) }.onSuccess { set(repo, pk, it) }.onFailure {
            if (it is SkychatHttpException) {
                disable(repo)
                return true
            }
        }
        return withTimeoutOrNull(timeoutMs) {
            while (repo.links.value[pk]?.ready != true) {
                delay(1_000)
                runCatching { repo.api.linkState(pk) }.getOrNull()?.let { set(repo, pk, it) }
            }
            true
        } ?: false
    }
}

/** Send text to the open thread: a note, a group post, or a DM (index.html sendMessage). */
fun ChatRepository.sendText(key: String, raw: String, reply: ReplyRef?) {
    val text = raw.trim()
    if (text.isEmpty()) return
    when {
        key == SAVED_KEY -> appendMessage(SAVED_KEY, ChatMessage(text = text, reply = reply, status = "sent"))
        isGroupKey(key) -> sendGroup(key, text, reply, forwarded = false, forwardFrom = "")
        else -> sendDm(key, text, reply, forwarded = false, forwardFrom = "", allowHold = true)
    }
    markRead(key)
}

internal fun ChatRepository.sendDm(
    pk: String,
    text: String,
    reply: ReplyRef?,
    forwarded: Boolean,
    forwardFrom: String,
    allowHold: Boolean,
    networkOverride: String? = null,
) {
    var net = networkOverride ?: network.value
    if (net == "cxo" && pk !in pair.value.paired) net = "skynet"
    val wire = when {
        reply != null -> SkychatClient.replyEnvelope(text, reply.copy(sender = if (reply.sender == ChatMessage.ME) myPk.value else reply.sender))
        forwarded -> SkychatClient.forwardEnvelope(text, forwardFrom)
        else -> text
    }
    val hold = allowHold && net != "cxo" && !linkOff && links.value[pk]?.ready != true
    val msg = ChatMessage(
        text = text, reply = reply, forwarded = forwarded, forwardFrom = forwardFrom,
        viaPair = net == "cxo", status = if (hold) "queued" else "pending",
    )
    ensureRecipient(pk)
    appendMessage(pk, msg)
    // Replying to a request lets them in; the visor does the same when it goes out.
    privacy.update { p -> p?.copy(requests = p.requests - pk) }
    if (hold) {
        outbox.getOrPut(pk) { mutableListOf() } += ChatRepository.Outgoing(msg.key, wire, net)
        LinkWatch.watch(this, pk)
        return
    }
    scope.launch { deliverDm(pk, msg.key, wire, net) }
}

internal suspend fun ChatRepository.deliverDm(pk: String, msgKey: String, wire: String, net: String) {
    try {
        val answer = if (net == "cxo") api.sendPair(pk, wire) else api.sendDm(pk, wire, net)
        updateMessage(pk, msgKey) { m ->
            m.copy(wireId = m.wireId.ifEmpty { answer.id }, status = SseDispatch.advance(m.status, "sent"))
        }
        if (answer.id.isNotEmpty()) SseDispatch.claimOrphan(this, pk, msgKey, answer.id)
    } catch (e: Exception) {
        // The mirror on /sse may already have proved it went out: a lost answer is not a failure.
        val m = findMessage(pk, msgKey)
        if (m != null && m.wireId.isNotEmpty()) return
        updateMessage(pk, msgKey) { it.copy(status = SseDispatch.advance(it.status, "failed")) }
        fail(R.string.chat_note_failed, e)
    }
}

/** Send what waited for the link, one at a time, in typing order. */
internal fun ChatRepository.flushOutbox(pk: String) {
    val held = outbox.remove(pk) ?: return
    scope.launch {
        for (o in held) {
            updateMessage(pk, o.msgKey) { it.copy(status = "pending") }
            deliverDm(pk, o.msgKey, o.wire, o.network)
        }
    }
}

fun ChatRepository.sendAnyway(pk: String) = flushOutbox(pk)

fun ChatRepository.retryLink(pk: String) = LinkWatch.retry(this, pk)

/** The route changed: dial again over it (or stop watching for CXO). */
fun ChatRepository.rewatchLink(pk: String) {
    LinkWatch.watch(this, null)
    LinkWatch.watch(this, pk)
}

internal fun ChatRepository.sendGroup(gid: String, text: String, reply: ReplyRef?, forwarded: Boolean, forwardFrom: String) {
    val wire = when {
        reply != null -> SkychatClient.replyEnvelope(text, reply.copy(sender = if (reply.sender == ChatMessage.ME) myPk.value else reply.sender))
        forwarded -> SkychatClient.forwardEnvelope(text, forwardFrom)
        else -> text
    }
    val msg = ChatMessage(text = text, reply = reply, forwarded = forwarded, forwardFrom = forwardFrom, status = "pending")
    appendMessage(gid, msg)
    scope.launch { deliverGroup(gid, msg.key, wire) }
}

private suspend fun ChatRepository.deliverGroup(gid: String, msgKey: String, wire: String) {
    try {
        api.groupSend(gid, wire)
        updateMessage(gid, msgKey) { it.copy(status = SseDispatch.advance(it.status, "sent")) }
    } catch (e: Exception) {
        val m = findMessage(gid, msgKey)
        if (m != null && m.tsNano.isNotEmpty()) return
        updateMessage(gid, msgKey) { it.copy(status = SseDispatch.advance(it.status, "failed")) }
        fail(R.string.chat_note_group_send_failed, e)
    }
}

/** ⚠ Resend on a failed text bubble; the reply is re-encoded from the bubble itself. */
fun ChatRepository.resend(key: String, msgKey: String) {
    val m = findMessage(key, msgKey) ?: return
    if (m.file != null) {
        retryFile(key, msgKey)
        return
    }
    updateMessage(key, msgKey) { it.copy(status = "pending") }
    if (isGroupKey(key)) {
        val wire = when {
            m.reply != null -> SkychatClient.replyEnvelope(m.text, m.reply.copy(sender = if (m.reply.sender == ChatMessage.ME) myPk.value else m.reply.sender))
            m.forwarded -> SkychatClient.forwardEnvelope(m.text, m.forwardFrom)
            else -> m.text
        }
        scope.launch { deliverGroup(key, msgKey, wire) }
    } else {
        val net = if (m.viaPair && key in pair.value.paired) "cxo" else network.value.let { if (it == "cxo") "skynet" else it }
        val wire = when {
            m.reply != null -> SkychatClient.replyEnvelope(m.text, m.reply.copy(sender = if (m.reply.sender == ChatMessage.ME) myPk.value else m.reply.sender))
            m.forwarded -> SkychatClient.forwardEnvelope(m.text, m.forwardFrom)
            else -> m.text
        }
        scope.launch { deliverDm(key, msgKey, wire, net) }
    }
}

// --- files ---

/** A picked document, copied to the cache so the upload can be retried and previewed. */
data class Picked(val file: File, val name: String, val mime: String)

suspend fun ChatRepository.pick(uri: Uri): Picked? = withContext(Dispatchers.IO) {
    runCatching {
        val resolver = app.contentResolver
        var name = "file"
        resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
            if (c.moveToFirst()) c.getString(0)?.let { name = it }
        }
        val mime = resolver.getType(uri)
            ?: MimeTypeMap.getSingleton().getMimeTypeFromExtension(name.substringAfterLast('.', "").lowercase())
            ?: "application/octet-stream"
        if (!name.contains('.')) {
            MimeTypeMap.getSingleton().getExtensionFromMimeType(mime)?.let { name = "$name.$it" }
        }
        val dir = File(app.cacheDir, "chat-up/" + UUID.randomUUID()).apply { mkdirs() }
        val out = File(dir, name.replace('/', '_'))
        resolver.openInputStream(uri)!!.use { input -> out.outputStream().use { input.copyTo(it) } }
        Picked(out, name, mime)
    }.getOrNull()
}

/**
 * Upload [file] to the thread [key] (index.html _uploadFile). [caption] is
 * the bubble's local text — the recorded-clip captions; the wire carries none.
 */
fun ChatRepository.sendFile(key: String, file: File, name: String, mime: String, caption: String? = null, recorded: Int = 0) {
    val msg = ChatMessage(
        text = caption ?: name,
        file = ChatFile(name = name, size = file.length(), status = "sending", localPath = file.absolutePath),
    )
    if (isPk(key)) ensureRecipient(key)
    appendMessage(key, msg)
    markRead(key)
    scope.launch { upload(key, msg.key, file, name, mime, recorded) }
}

fun ChatRepository.retryFile(key: String, msgKey: String) {
    val m = findMessage(key, msgKey) ?: return
    val f = m.file ?: return
    val local = File(f.localPath)
    if (f.status != "failed" || !local.isFile) return
    val mime = MimeTypeMap.getSingleton().getMimeTypeFromExtension(f.name.substringAfterLast('.', "").lowercase()) ?: "application/octet-stream"
    updateMessage(key, msgKey) { it.copy(file = f.copy(status = "sending")) }
    scope.launch { upload(key, msgKey, local, f.name, mime, 0) }
}

/** [recorded]: 0 an attachment, 1 a voice message, 2 a video message — only for the toast. */
private suspend fun ChatRepository.upload(key: String, msgKey: String, file: File, name: String, mime: String, recorded: Int) {
    val saved = key == SAVED_KEY
    val (field, value) = when {
        saved -> "local" to "1"
        isGroupKey(key) -> "group" to key
        else -> "pk" to key
    }
    // A 1:1 file is accepted only over a live chat conn (filexfer.go), so open the link first.
    if (field == "pk") LinkWatch.await(this, key, LINK_WAIT_MS)
    try {
        val res = api.sendFile(field, value, name, mime, file.length(), { file.inputStream() }) { sent, total ->
            uploads.update { it + (msgKey to (sent to total)) }
        }
        updateMessage(key, msgKey) { m ->
            val f = m.file ?: return@updateMessage m
            m.copy(file = f.copy(id = res.id.ifEmpty { f.id }, url = res.fileUrl.ifEmpty { f.url }, status = if (saved) "stored" else "sent"))
        }
        if (res.id.isNotEmpty()) SseDispatch.claimOrphan(this, key, msgKey, res.id)
        note(
            when {
                recorded == 1 -> if (saved) R.string.chat_note_voice_saved else R.string.chat_note_voice_sent
                recorded == 2 -> if (saved) R.string.chat_note_video_saved else R.string.chat_note_video_sent
                saved -> R.string.chat_note_saved
                else -> R.string.chat_note_file_sent
            },
        )
    } catch (e: Exception) {
        updateMessage(key, msgKey) { m -> m.file?.let { m.copy(file = it.copy(status = "failed")) } ?: m }
        fail(if (saved) R.string.chat_note_save_file_failed else R.string.chat_note_file_failed, e)
    } finally {
        delay(UPLOAD_FADE_MS)
        uploads.update { it - msgKey }
    }
}

fun ChatRepository.requestFile(m: ChatMessage) {
    val f = m.file ?: return
    if (f.from.isEmpty() || f.id.isEmpty()) return
    scope.launch {
        runCatching { api.requestFile(f.from, f.id, f.name) }
            .onSuccess { note(R.string.chat_note_requested_file) }
            .onFailure { fail(R.string.chat_note_request_failed, it) }
    }
}

fun ChatRepository.download(m: ChatMessage) {
    val f = m.file ?: return
    if (!f.url.startsWith("/files/")) return
    scope.launch {
        runCatching { media.saveToDownloads(f.url, f.name, null) }
            .onSuccess { note(R.string.chat_note_downloading, f.name) }
            .onFailure { fail(R.string.chat_note_failed, it) }
    }
}

// --- deleting ---

fun ChatRepository.deleteForMe(key: String, m: ChatMessage) {
    mutate(key) { list -> list.filterNot { it.key == m.key } }
    when {
        key == SAVED_KEY -> Unit
        isGroupKey(key) -> if (m.tsNano.isNotEmpty()) {
            store.update { s -> s.copy(hiddenGroup = s.hiddenGroup + "$key:${m.tsNano}") }
        }
        else -> m.dmWireId.takeIf { it.isNotEmpty() }?.let { id -> scope.launch { runCatching { api.forget(key, id) } } }
    }
}

/** Own messages only: a group needs its feed id, a DM its wire id. */
fun ChatRepository.canDeleteForEveryone(key: String, m: ChatMessage): Boolean {
    return m.mine && key != SAVED_KEY
}

fun ChatRepository.deleteForEveryone(key: String, m: ChatMessage) {
    if (isGroupKey(key)) {
        if (m.tsNano.isEmpty()) {
            note(R.string.chat_note_not_confirmed, error = true)
            return
        }
        store.update { s -> s.copy(hiddenGroup = s.hiddenGroup + "$key:${m.tsNano}") }
        mutate(key) { list -> list.filterNot { it.key == m.key } }
        scope.launch { runCatching { api.groupDeleteMessage(key, m.tsNano) }.onFailure { fail(R.string.chat_note_delete_failed, it) } }
        return
    }
    val id = m.dmWireId
    if (id.isEmpty()) {
        note(R.string.chat_note_not_confirmed, error = true)
        return
    }
    mutate(key) { list -> list.filterNot { it.key == m.key } }
    scope.launch {
        runCatching { if (m.viaPair) api.deletePair(key, id) else api.deleteDm(key, id) }
            .onFailure { fail(R.string.chat_note_delete_failed, it) }
    }
}

// --- forward / save ---

/** Where a forward can go, in the picker's order. */
data class ForwardTarget(val key: String, val label: String, val sub: String, val kind: Int)

/** Forwarding sends text only; out of a channel it may name the channel (forward.go). */
fun ChatRepository.forward(fromKey: String, m: ChatMessage, toKey: String) {
    val text = m.text.ifEmpty { m.file?.name.orEmpty() }.ifEmpty { app.getString(R.string.chat_attachment) }
    if (text.isBlank()) {
        note(R.string.chat_note_nothing_forward, error = true)
        return
    }
    val origin = groupInfo(fromKey)?.takeIf { it.isChannel }?.name.orEmpty()
    when {
        toKey == SAVED_KEY -> {
            appendMessage(SAVED_KEY, ChatMessage(text = text, forwarded = true, forwardFrom = origin, status = "sent"))
            note(R.string.chat_note_forwarded_saved)
        }
        isGroupKey(toKey) -> {
            sendGroup(toKey, text, null, forwarded = true, forwardFrom = origin)
            note(R.string.chat_note_forwarded)
        }
        else -> {
            sendDm(toKey, text, null, forwarded = true, forwardFrom = origin, allowHold = false)
            note(R.string.chat_note_forwarded)
        }
    }
}

fun ChatRepository.saveToSaved(m: ChatMessage) {
    val who = if (m.mine) app.getString(R.string.chat_you) else displayName(m.from)
    val body = m.text.ifEmpty { m.file?.name.orEmpty() }.ifEmpty { app.getString(R.string.chat_attachment) }
    appendMessage(SAVED_KEY, ChatMessage(text = "$who: $body", status = "sent"))
    note(R.string.chat_note_saved_to_saved)
}

private const val LINK_WAIT_MS = 25_000L
private const val UPLOAD_FADE_MS = 1_400L
