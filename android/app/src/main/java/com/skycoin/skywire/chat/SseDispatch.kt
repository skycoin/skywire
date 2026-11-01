package com.skycoin.skywire.chat

import com.skycoin.skywire.R
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

/** Applies one `/sse` event to the repository (index.html _sseSubscribe, in the same order). */
internal object SseDispatch {

    private const val HEARTBEAT = "__skychat_group_heartbeat__"
    private const val ORPHANS = 64

    fun handle(repo: ChatRepository, m: SseMessage) {
        // A replay is history the app loads itself, except a delete: a message
        // deleted while the app was away leaves no history to say so.
        val delete = (m.channel == "dm-status" && m.status == "deleted") || m.channel == "group-delete"
        if (m.history != null && !delete) return
        when (m.channel) {
            "pair-invite" -> pairInvite(repo, m)
            "group" -> group(repo, m)
            "group-delete" -> groupDelete(repo, m)
            "group-join-request" -> joinRequest(repo, m)
            "group-update" -> groupUpdate(repo, m)
            "file-ready" -> fileReady(repo, m)
            "dm-status" -> dmStatus(repo, m)
            "pair" -> dm(repo, m, viaPair = true)
            "" -> if (m.dir == "out") outbound(repo, m) else dm(repo, m, viaPair = false)
        }
    }

    private fun dm(repo: ChatRepository, m: SseMessage, viaPair: Boolean) {
        val sender = m.sender.lowercase()
        val me = repo.myPk.value
        if (!isPk(sender) || sender == me) return
        if (m.id.isNotEmpty() && repo.thread(sender).value.any { it.dmWireId == m.id }) return
        val (text, reply, forwarded, forwardFrom) = if (viaPair) unwrap(m.message) else Unwrapped(
            m.message,
            repo.replyOf(m.replyToSender, m.replyToTs, m.replyToPreview),
            m.forwarded,
            m.forwardFrom,
        )
        val file = if (m.fileId.isNotEmpty()) {
            ChatFile(id = m.fileId, name = m.fileName.ifEmpty { "file" }, size = m.fileSize, status = m.fileStatus, url = m.fileUrl, from = sender)
        } else {
            null
        }
        val msg = ChatMessage(
            ts = System.currentTimeMillis(),
            from = sender,
            text = text,
            viaPair = viaPair,
            file = file,
            reply = reply,
            forwarded = forwarded,
            forwardFrom = forwardFrom,
            wireId = m.id,
            request = m.request,
        )
        if (m.request) {
            repo.privacy.update { p -> p?.copy(requests = (p.requests + sender).distinct()) }
        }
        repo.ensureRecipient(sender)
        repo.appendMessage(sender, msg)
        if (repo.openKey.value == sender && repo.atBottom && com.skycoin.skywire.core.AppVisibility.isForeground.value) {
            repo.markRead(sender)
            repo.scope.launch { repo.sendReadReceipts(sender) }
        }
    }

    /**
     * Our own send, mirrored before `/message` answers. It carries the id,
     * which matters when the response itself is lost to the server's 10 s
     * write deadline. Outgoing call records only ever arrive this way.
     */
    private fun outbound(repo: ChatRepository, m: SseMessage) {
        if (m.network == "voice") {
            val peer = m.sender.lowercase()
            if (!isPk(peer) || repo.thread(peer).value.any { it.wireId == m.id }) return
            repo.ensureRecipient(peer)
            repo.appendMessage(peer, ChatMessage(from = ChatMessage.ME, text = m.message, wireId = m.id, status = "sent"))
            return
        }
        val peer = m.to.lowercase()
        if (!isPk(peer) || m.id.isEmpty() || m.fileId.isNotEmpty()) return
        val list = repo.thread(peer).value
        if (list.any { it.wireId == m.id }) return
        val target = list.lastOrNull { it.mine && it.wireId.isEmpty() && it.file == null && it.text == m.message && it.status in setOf("pending", "failed", "queued") }
        if (target == null) {
            // Sent from elsewhere on this visor (the CLI): it is still ours.
            repo.ensureRecipient(peer)
            repo.appendMessage(
                peer,
                ChatMessage(
                    from = ChatMessage.ME, text = m.message, wireId = m.id, status = "sent",
                    reply = repo.replyOf(m.replyToSender, m.replyToTs, m.replyToPreview),
                    forwarded = m.forwarded, forwardFrom = m.forwardFrom,
                ),
            )
            return
        }
        repo.updateMessage(peer, target.key) { it.copy(wireId = m.id, status = advance(it.status, "sent")) }
        claimOrphan(repo, peer, target.key, m.id)
    }

    private fun dmStatus(repo: ChatRepository, m: SseMessage) {
        val peer = m.peer.lowercase()
        if (m.status == "deleted") {
            repo.mutate(peer) { list -> list.filterNot { it.dmWireId == m.id }.let { if (it.size == list.size) list else it } }
            return
        }
        val target = repo.thread(peer).value.lastOrNull { it.mine && (it.wireId == m.id || it.file?.id == m.id) }
        if (target == null) {
            repo.orphanStatus[m.id] = m.status
            while (repo.orphanStatus.size > ORPHANS) repo.orphanStatus.remove(repo.orphanStatus.keys.first())
            return
        }
        repo.updateMessage(peer, target.key) { it.copy(status = advance(it.status, m.status)) }
    }

    /** A receipt that beat the send's answer is applied once the bubble knows its id. */
    fun claimOrphan(repo: ChatRepository, key: String, msgKey: String, id: String) {
        val status = repo.orphanStatus.remove(id) ?: return
        repo.updateMessage(key, msgKey) { it.copy(status = advance(it.status, status)) }
    }

    /** Statuses only move forward; failed only replaces nothing or pending. */
    fun advance(current: String, next: String): String {
        if (next == "failed") return if (current.isEmpty() || current == "pending") "failed" else current
        if (current == "failed") return next
        return if (statusRank(next) > statusRank(current)) next else current
    }

    private fun fileReady(repo: ChatRepository, m: SseMessage) {
        for (key in repo.threads.keys.toList()) {
            val hit = repo.thread(key).value.any { it.file?.id == m.fileId }
            if (!hit) continue
            repo.mutate(key) { list ->
                list.map {
                    val f = it.file
                    if (f != null && f.id == m.fileId) it.copy(file = f.copy(url = m.fileUrl.ifEmpty { f.url }, status = "received")) else it
                }
            }
        }
    }

    private fun group(repo: ChatRepository, m: SseMessage) {
        if (m.message == HEARTBEAT) return
        val gid = m.groupId
        val sender = m.sender.lowercase()
        val nano = m.tsNano.nanoText()
        val me = repo.myPk.value
        if (repo.groupInfo(gid) == null) repo.scope.launch { repo.syncGroups() }
        if (nano.isNotEmpty() && "$gid:$nano" in repo.store.state.value.hiddenGroup) return
        val list = repo.thread(gid).value
        if (nano.isNotEmpty() && list.any { it.tsNano == nano && (it.from == sender || (it.mine && sender == me)) }) return
        if (sender == me) {
            // Our own post coming back: give the pending bubble its feed id.
            val text = m.message
            val own = list.lastOrNull { it.mine && it.tsNano.isEmpty() && (it.text == text || it.file?.id == m.fileId && m.fileId.isNotEmpty()) }
            if (own != null) {
                repo.updateMessage(gid, own.key) { it.copy(tsNano = nano, status = advance(it.status, "sent")) }
                return
            }
        }
        val file = if (m.fileId.isNotEmpty()) {
            ChatFile(id = m.fileId, name = m.fileName.ifEmpty { "file" }, size = m.fileSize, url = m.fileUrl, from = if (sender == me) "" else sender)
        } else {
            null
        }
        val msg = ChatMessage(
            ts = parseTime(m.ts) ?: System.currentTimeMillis(),
            from = if (sender == me) ChatMessage.ME else sender,
            text = m.message,
            file = file,
            reply = repo.replyOf(m.replyToSender, m.replyToTs, m.replyToPreview),
            forwarded = m.forwarded,
            forwardFrom = m.forwardFrom,
            tsNano = nano,
            status = if (sender == me) "sent" else "",
        )
        repo.appendMessage(gid, msg)
        val open = repo.openKey.value == gid && com.skycoin.skywire.core.AppVisibility.isForeground.value
        if (sender != me && !open) {
            repo.groupUnread.update { it + (gid to ((it[gid] ?: 0) + 1)) }
            repo.recompute(gid)
        }
    }

    private fun groupDelete(repo: ChatRepository, m: SseMessage) {
        val nano = m.deletedTsNano.nanoText()
        if (nano.isEmpty()) return
        val gid = m.groupId
        repo.store.update { s -> s.copy(hiddenGroup = s.hiddenGroup + "$gid:$nano") }
        val by = m.deletedBy.lowercase()
        val me = repo.myPk.value
        repo.mutate(gid) { list -> withoutDeleted(list, nano, by, me) }
    }

    /** A tombstone is author-signed: it removes only that author's message with that feed id. */
    fun withoutDeleted(list: List<ChatMessage>, nano: String, by: String, me: String): List<ChatMessage> {
        val kept = list.filterNot { it.tsNano == nano && (it.from == by || (it.mine && by == me)) }
        return if (kept.size == list.size) list else kept
    }

    /** Someone joined, left, was promoted or removed: skychat saw the group's info change. */
    private fun groupUpdate(repo: ChatRepository, m: SseMessage) {
        repo.scope.launch {
            repo.syncGroups()
            repo.groupChanged.tryEmit(m.groupId)
        }
    }

    private fun joinRequest(repo: ChatRepository, m: SseMessage) {
        repo.scope.launch {
            repo.syncGroups()
            repo.groupChanged.tryEmit(m.groupId)
        }
        val open = repo.openKey.value == m.groupId && com.skycoin.skywire.core.AppVisibility.isForeground.value
        if (!open) {
            if (m.groupName.isNotEmpty()) repo.note(R.string.chat_note_join_request_named, m.groupName) else repo.note(R.string.chat_note_join_request)
        }
    }

    private fun pairInvite(repo: ChatRepository, m: SseMessage) {
        val peer = m.peer.lowercase()
        val short = peer.take(8)
        when (m.event) {
            "received" -> {
                repo.pair.update { it.copy(invites = (it.invites + peer).distinct()) }
                repo.note(R.string.chat_note_pair_invite, short)
            }
            "accepted" -> {
                repo.pair.update { it.copy(paired = it.paired + peer) }
                repo.ensureRecipient(peer)
                repo.note(R.string.chat_note_pair_confirmed, short)
            }
            "declined" -> {
                repo.pair.update { it.copy(paired = it.paired - peer) }
                repo.note(R.string.chat_note_pair_declined, short, error = true)
            }
        }
        repo.scope.launch { repo.syncInvites() }
    }

    data class Unwrapped(val text: String, val reply: ReplyRef?, val forwarded: Boolean, val forwardFrom: String)

    /** CXO pair messages arrive with their reply/forward envelope still on; open it here. */
    fun unwrap(body: String): Unwrapped {
        val t = body.trim()
        if (!t.startsWith("{")) return Unwrapped(body, null, false, "")
        val obj = runCatching { SkychatClient.json.parseToJsonElement(t).jsonObject }.getOrNull()
            ?: return Unwrapped(body, null, false, "")
        (obj["skychat_reply"] as? JsonObject)?.let { r ->
            fun f(k: String) = runCatching { r[k]?.jsonPrimitive?.content }.getOrNull().orEmpty()
            return Unwrapped(f("text"), ReplyRef(f("to_sender").lowercase(), f("to_ts"), f("to_preview")), false, "")
        }
        (obj["skychat_forward"] as? JsonObject)?.let { r ->
            fun f(k: String) = runCatching { r[k]?.jsonPrimitive?.content }.getOrNull().orEmpty()
            return Unwrapped(f("text"), null, true, f("from"))
        }
        return Unwrapped(body, null, false, "")
    }
}
