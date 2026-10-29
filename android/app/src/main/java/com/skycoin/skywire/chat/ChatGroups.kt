package com.skycoin.skywire.chat

import com.skycoin.skywire.R
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

private const val CHUNK = 50
private const val META_THROTTLE_MS = 5 * 60_000L

/** The first page of a group, unless it was already loaded (index.html selectGroup). */
internal suspend fun ChatRepository.openGroup(gid: String) {
    markRead(gid)
    maybeRefreshMeta(gid)
    if (hist.value[gid]?.loaded == true) return
    loadGroupPage(gid, older = false)
}

/** After a reconnect: pull the newest page again so a gap in the stream fills in. */
internal suspend fun ChatRepository.refreshGroupHead(gid: String) {
    if (hist.value[gid]?.loaded != true) return
    val rows = runCatching { api.groupHistory(gid, CHUNK, null) }.getOrNull() ?: return
    mergeGroupRows(gid, rows)
}

/** Older backlog as the reader scrolls up (index.html loadOlderGroupHistory). */
fun ChatRepository.loadOlder(gid: String) {
    val page = hist.value[gid] ?: return
    if (!page.loaded || page.done || page.loading) return
    scope.launch { loadGroupPage(gid, older = true) }
}

private suspend fun ChatRepository.loadGroupPage(gid: String, older: Boolean) {
    var page = hist.value[gid] ?: HistPage()
    if (page.loading) return
    while (true) {
        hist.update { it + (gid to page.copy(loading = true)) }
        val rows = try {
            api.groupHistory(gid, CHUNK, if (older) page.cursor else null)
        } catch (e: Exception) {
            hist.update { it + (gid to page.copy(loading = false)) }
            return
        }
        val cursor = rows.firstOrNull()?.let { r -> r.tsNano.nanoText().ifEmpty { parseTime(r.ts)?.let { (it * 1_000_000).toString() } } }
            ?: page.cursor
        page = HistPage(cursor = cursor, done = rows.size < CHUNK, loading = false, loaded = true)
        hist.update { it + (gid to page) }
        val shown = mergeGroupRows(gid, rows)
        // A page that was entirely deleted-for-me shows nothing; ask for the next one.
        if (!older || shown > 0 || page.done) return
    }
}

/** Returns how many rows were visible. */
private fun ChatRepository.mergeGroupRows(gid: String, rows: List<GroupHistoryRecord>): Int {
    val me = myPk.value
    val hidden = store.state.value.hiddenGroup
    val incoming = rows.mapNotNull { r ->
        val nano = r.tsNano.nanoText()
        if (r.text == "__skychat_group_heartbeat__") return@mapNotNull null
        if (nano.isNotEmpty() && "$gid:$nano" in hidden) return@mapNotNull null
        val sender = r.senderPk.lowercase()
        val mine = sender == me
        ChatMessage(
            key = "g:$gid:$nano:$sender",
            ts = parseTime(r.ts) ?: return@mapNotNull null,
            from = if (mine) ChatMessage.ME else sender,
            text = r.text,
            file = if (r.fileId.isNotEmpty()) {
                ChatFile(id = r.fileId, name = r.fileName.ifEmpty { "file" }, size = r.fileSize, url = r.fileUrl, from = if (mine) "" else sender)
            } else {
                null
            },
            reply = replyOf(r.replyToSender, r.replyToTs, r.replyToPreview),
            forwarded = r.forwarded,
            forwardFrom = r.forwardFrom,
            tsNano = nano,
            status = if (mine) "sent" else "",
        )
    }
    if (incoming.isEmpty()) return 0
    mutate(gid) { current ->
        val known = current.mapNotNull { m -> m.tsNano.takeIf { it.isNotEmpty() } }.toHashSet()
        val fresh = incoming.filter { it.tsNano.isEmpty() || it.tsNano !in known }
        if (fresh.isEmpty()) current else (current + fresh).sortedBy { it.ts }
    }
    return incoming.size
}

/** A member's copy of the founder's name and picture, refreshed at most every few minutes. */
private suspend fun ChatRepository.maybeRefreshMeta(gid: String) {
    val g = groupInfo(gid) ?: return
    if (g.ownerPk.equals(myPk.value, ignoreCase = true)) return
    val now = System.currentTimeMillis()
    if (now - (groupMetaAskedAt[gid] ?: 0L) < META_THROTTLE_MS) return
    groupMetaAskedAt[gid] = now
    runCatching { api.refreshMeta(gid) }.onSuccess { applyMeta(gid, it) }
}

/** Only name and picture move; the rest of the cached record stays live. */
internal fun ChatRepository.applyMeta(gid: String, info: GroupInfo) {
    groups.update { list -> list.map { if (it.id == gid) it.copy(name = info.name.ifEmpty { it.name }, avatar = info.avatar) else it } }
}

internal fun ChatRepository.replaceGroup(info: GroupInfo) {
    if (info.id.isEmpty()) return
    groups.update { list -> if (list.any { it.id == info.id }) list.map { if (it.id == info.id) info else it } else list + info }
    groupChanged.tryEmit(info.id)
}

fun ChatRepository.isAdmin(g: GroupInfo): Boolean {
    val me = myPk.value
    return g.ownerPk.equals(me, true) || g.admins.any { it.equals(me, true) }
}

fun ChatRepository.isFounder(g: GroupInfo): Boolean = g.ownerPk.equals(myPk.value, true)

/** Create a group or channel; returns the new id. The invite link goes to the clipboard. */
suspend fun ChatRepository.createGroup(name: String, kind: String, peerBackfill: Boolean?, listed: Boolean?): Pair<String, String>? =
    runCatching { api.createGroup(name, kind, peerBackfill, listed) }
        .onFailure { fail(R.string.chat_note_create_failed, it) }
        .getOrNull()
        ?.let { created ->
            syncGroups()
            threadFlow(created.info.id)
            created.info.id to created.invite
        }

/** Join from a resolved address; returns the group once admitted, or null (waiting or failed). */
suspend fun ChatRepository.join(res: Resolved): GroupInfo? {
    val info = runCatching { api.join(res.invite.ifEmpty { null }, res.address) }
        .onFailure { fail(R.string.chat_note_join_failed, it) }
        .getOrNull() ?: return null
    syncGroups()
    if (info.status == "awaiting_approval") {
        note(R.string.chat_note_request_sent)
        return null
    }
    note(if (res.group?.kind == "channel" || info.isChannel) R.string.chat_note_joined_channel else R.string.chat_note_joined_group)
    return info
}

fun ChatRepository.memberAction(gid: String, action: String, pk: String) {
    scope.launch {
        runCatching { api.groupMemberAction(gid, action, pk) }
            .onSuccess {
                replaceGroup(it)
                note(R.string.chat_note_done)
            }
            .onFailure { fail(R.string.chat_note_failed, it) }
    }
}

fun ChatRepository.setReadOnly(gid: String, on: Boolean) = groupSetting(
    gid,
    { api.setReadOnly(gid, on) },
    if (on) R.string.chat_note_read_only_on else R.string.chat_note_read_only_off,
)

fun ChatRepository.setPeerBackfill(gid: String, on: Boolean) = groupSetting(
    gid,
    { api.setPeerBackfill(gid, on) },
    if (on) R.string.chat_note_backfill_on else R.string.chat_note_backfill_off,
)

fun ChatRepository.setJoinCost(gid: String, bits: Int) = groupSetting(
    gid,
    { api.setJoinPow(gid, bits) },
    if (bits == 0) R.string.chat_note_join_free else R.string.chat_note_join_cost,
)

fun ChatRepository.rotateKey(gid: String, done: () -> Unit) {
    scope.launch {
        runCatching { api.rotateKey(gid) }
            .onSuccess {
                replaceGroup(it)
                note(R.string.chat_note_rekeyed, it.keyEpoch)
            }
            .onFailure { fail(R.string.chat_note_rotate_failed, it) }
        done()
    }
}

fun ChatRepository.setListed(gid: String, on: Boolean) {
    scope.launch {
        runCatching { api.setListed(gid, on) }
            .onSuccess {
                syncGroups()
                groupChanged.tryEmit(gid)
                note(if (on) R.string.chat_note_listed_on else R.string.chat_note_listed_off)
            }
            .onFailure {
                groupChanged.tryEmit(gid)
                fail(R.string.chat_note_failed, it)
            }
    }
}

private fun ChatRepository.groupSetting(gid: String, call: suspend () -> GroupInfo, ok: Int) {
    scope.launch {
        runCatching { call() }
            .onSuccess {
                replaceGroup(it)
                note(ok)
            }
            .onFailure {
                groupChanged.tryEmit(gid)
                fail(R.string.chat_note_failed, it)
            }
    }
}

fun ChatRepository.setGroupName(gid: String, name: String) {
    if (name.isBlank()) {
        note(R.string.chat_note_group_needs_name, error = true)
        return
    }
    scope.launch {
        runCatching { api.setGroupMeta(gid, name = name.trim()) }
            .onSuccess {
                applyMeta(gid, it)
                groupChanged.tryEmit(gid)
                note(R.string.chat_note_renamed)
            }
            .onFailure { fail(R.string.chat_note_failed, it) }
    }
}

/** [dataUrl] empty clears the picture. */
fun ChatRepository.setGroupAvatar(gid: String, dataUrl: String) {
    val channel = groupInfo(gid)?.isChannel == true
    scope.launch {
        runCatching { api.setGroupMeta(gid, avatar = dataUrl) }
            .onSuccess {
                applyMeta(gid, it)
                groupChanged.tryEmit(gid)
                note(
                    when {
                        dataUrl.isEmpty() -> R.string.chat_note_picture_removed
                        channel -> R.string.chat_note_channel_picture
                        else -> R.string.chat_note_group_picture
                    },
                )
            }
            .onFailure { fail(R.string.chat_note_failed, it) }
    }
}

suspend fun ChatRepository.inviteLink(gid: String): String? =
    runCatching { api.invite(gid) }.onFailure { note(R.string.chat_note_no_invite, error = true) }.getOrNull()

fun ChatRepository.leaveGroup(gid: String, done: () -> Unit) {
    val channel = groupInfo(gid)?.isChannel == true
    scope.launch {
        runCatching { api.leave(gid) }
            .onSuccess {
                removeGroup(gid)
                note(if (channel) R.string.chat_note_left_channel else R.string.chat_note_left_group)
                done()
            }
            .onFailure { fail(R.string.chat_note_failed, it) }
    }
}

fun ChatRepository.deleteGroup(gid: String, done: () -> Unit) {
    val channel = groupInfo(gid)?.isChannel == true
    scope.launch {
        runCatching { api.deleteGroup(gid) }
            .onSuccess {
                removeGroup(gid)
                note(if (channel) R.string.chat_note_channel_deleted else R.string.chat_note_group_deleted)
                done()
            }
            .onFailure { fail(R.string.chat_note_failed, it) }
    }
}

private fun ChatRepository.removeGroup(gid: String) {
    groups.update { list -> list.filterNot { it.id == gid } }
    unpinIf("grp:$gid")
    summaries.update { it - gid }
    hist.update { it - gid }
}

fun ChatRepository.unpinIf(pin: String) {
    if (store.state.value.pinned == pin) store.update { it.copy(pinned = null) }
}
