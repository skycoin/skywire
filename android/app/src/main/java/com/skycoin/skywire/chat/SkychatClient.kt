package com.skycoin.skywire.chat

import kotlinx.serialization.KSerializer
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.builtins.MapSerializer
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonObject
import java.io.InputStream
import java.net.URLEncoder

/**
 * Every skychat endpoint the chat screens use, typed. A thin layer: the
 * rules about when to call what live in [ChatRepository].
 */
class SkychatClient(val http: SkychatHttp) {

    private fun enc(s: String): String = URLEncoder.encode(s, "UTF-8")

    private fun <T> decode(serializer: KSerializer<T>, body: String): T = json.decodeFromString(serializer, body)

    /** Go encodes an empty slice as `null`; that is an empty list here. */
    private fun <T> decodeList(serializer: KSerializer<T>, body: String): List<T> {
        val t = body.trim()
        if (t.isEmpty() || t == "null") return emptyList()
        return json.decodeFromString(ListSerializer(serializer), t)
    }

    private fun obj(build: kotlinx.serialization.json.JsonObjectBuilder.() -> Unit): String =
        buildJsonObject(build).toString()

    // --- identity, history ---

    suspend fun status(): SkychatStatus = decode(SkychatStatus.serializer(), http.get("/status"))

    suspend fun history(peer: String, limit: Int = 100): List<HistoryRecord> =
        decodeList(HistoryRecord.serializer(), http.get("/history?peer=${enc(peer)}&limit=$limit"))

    /** The newest stored DMs across every peer, oldest first: what a reconnect catches up from. */
    suspend fun recentHistory(limit: Int): List<HistoryRecord> =
        decodeList(HistoryRecord.serializer(), http.get("/history?limit=$limit"))

    suspend fun historyPeers(): List<String> =
        decodeList(String.serializer(), http.get("/history/peers"))

    suspend fun forget(pk: String, id: String? = null) {
        http.post("/history/forget", obj { put("pk", pk); if (id == null) put("all", true) else put("id", id) })
    }

    suspend fun search(q: String): List<SearchHit> =
        decodeList(SearchHit.serializer(), http.get("/search?q=${enc(q)}"))

    suspend fun reportUnread(n: Int) {
        http.post("/unread", obj { put("unread", n) })
    }

    suspend fun notifyFocus(key: String) {
        http.post("/notify-focus", obj { put("key", key); put("focused", key.isNotEmpty()) })
    }

    // --- DMs ---

    suspend fun sendDm(pk: String, wire: String, network: String): SendAnswer = decode(
        SendAnswer.serializer(),
        http.post(
            "/message",
            obj { put("recipient", pk); put("message", wire); put("network", network); put("receipts", true) },
            slowPeer = true,
        ),
    )

    suspend fun sendPair(pk: String, wire: String): SendAnswer =
        decode(SendAnswer.serializer(), http.post("/pair/$pk/message", obj { put("text", wire) }, slowPeer = true))

    suspend fun readReceipt(pk: String, ids: List<String>) {
        http.post("/read-receipt", obj { put("pk", pk); put("ids", buildJsonArray { ids.forEach { add(kotlinx.serialization.json.JsonPrimitive(it)) } }) })
    }

    suspend fun deleteDm(pk: String, id: String) {
        http.post("/delete", obj { put("pk", pk); put("id", id) }, slowPeer = true)
    }

    suspend fun deletePair(pk: String, id: String) {
        http.delete("/pair/$pk/message?id=${enc(id)}")
    }

    suspend fun linkStart(pk: String, network: String): LinkState =
        decode(LinkState.serializer(), http.post("/link", obj { put("pk", pk); put("network", network) }))

    suspend fun linkState(pk: String): LinkState = decode(LinkState.serializer(), http.get("/link?pk=${enc(pk)}"))

    // --- files ---

    /** [dest] is one of `pk`, `group` or `local` (Saved Messages). */
    suspend fun sendFile(
        destField: String,
        destValue: String,
        name: String,
        mime: String,
        size: Long,
        open: () -> InputStream,
        progress: (Long, Long) -> Unit,
    ): FileSent {
        val body = http.upload("/send-file", mapOf(destField to destValue), "file", name, mime, size, open, progress)
        return runCatching { decode(FileSent.serializer(), body) }.getOrDefault(FileSent())
    }

    suspend fun requestFile(pk: String, fileId: String, fileName: String) {
        http.post("/request-file", obj { put("pk", pk); put("file_id", fileId); put("file_name", fileName) })
    }

    // --- groups ---

    suspend fun groups(): List<GroupInfo> = decodeList(GroupInfo.serializer(), http.get("/group"))

    suspend fun groupHistory(gid: String, limit: Int, before: String?): List<GroupHistoryRecord> = decodeList(GroupHistoryRecord.serializer(),
        http.get("/group/$gid/history?limit=$limit" + (before?.let { "&before=${enc(it)}" } ?: "")),
    )

    suspend fun groupSend(gid: String, wire: String) {
        http.post("/group/$gid/send", obj { put("text", wire) }, slowPeer = true)
    }

    suspend fun groupDeleteMessage(gid: String, tsNano: String) {
        http.delete("/group/$gid/message?ts=${enc(tsNano)}")
    }

    suspend fun createGroup(name: String, kind: String, peerBackfill: Boolean?, listed: Boolean?): GroupCreated =
        decode(
            GroupCreated.serializer(),
            http.post(
                "/group",
                obj {
                    put("name", name)
                    put("kind", kind)
                    put("members", buildJsonArray { })
                    peerBackfill?.let { put("peer_backfill", it) }
                    listed?.let { put("listed", it) }
                },
                slowPeer = true,
            ),
        )

    suspend fun resolve(address: String): Resolved =
        decode(Resolved.serializer(), http.get("/group/resolve?address=${enc(address)}", slowPeer = true))

    suspend fun catalog(pk: String): Catalog = decode(Catalog.serializer(), http.get("/group/catalog?pk=${enc(pk)}", slowPeer = true))

    suspend fun join(invite: String?, address: String?): GroupInfo {
        val body = http.post(
            "/group/join",
            obj { if (!invite.isNullOrEmpty()) put("invite", invite) else put("address", address.orEmpty()) },
            slowPeer = true,
        )
        return decodeGroupAnswer(body)
    }

    suspend fun groupRequests(gid: String): List<JoinRequest> =
        decodeList(JoinRequest.serializer(), http.get("/group/$gid/requests"))

    /** `members/promote` … `requests/deny`; answers with the updated group. */
    suspend fun groupMemberAction(gid: String, action: String, pk: String): GroupInfo =
        decodeGroupAnswer(http.post("/group/$gid/$action", obj { put("pk", pk) }, slowPeer = true))

    suspend fun rotateKey(gid: String): GroupInfo = decodeGroupAnswer(http.post("/group/$gid/rotate-key", slowPeer = true))

    suspend fun setJoinPow(gid: String, bits: Int): GroupInfo =
        decodeGroupAnswer(http.post("/group/$gid/join-pow", obj { put("bits", bits) }))

    suspend fun setPeerBackfill(gid: String, enabled: Boolean): GroupInfo =
        decodeGroupAnswer(http.post("/group/$gid/peer-backfill", obj { put("enabled", enabled) }))

    suspend fun setGroupMeta(gid: String, name: String? = null, avatar: String? = null): GroupInfo =
        decodeGroupAnswer(
            http.post("/group/$gid/meta", obj { name?.let { put("name", it) }; avatar?.let { put("avatar", it) } }, slowPeer = true),
        )

    suspend fun refreshMeta(gid: String): GroupInfo = decodeGroupAnswer(http.post("/group/$gid/refresh-meta", slowPeer = true))

    suspend fun setListed(gid: String, listed: Boolean) {
        http.post("/group/$gid/listed", obj { put("listed", listed) })
    }

    suspend fun setReadOnly(gid: String, readOnly: Boolean): GroupInfo =
        decodeGroupAnswer(http.post("/group/$gid/readonly", obj { put("read_only", readOnly) }, slowPeer = true))

    suspend fun invite(gid: String): String = decode(GroupInvite.serializer(), http.get("/group/$gid/invite")).invite

    suspend fun leave(gid: String) {
        http.post("/group/$gid/leave", slowPeer = true)
    }

    suspend fun deleteGroup(gid: String) {
        http.delete("/group/$gid")
    }

    /** Some actions answer `{info: …}`, others the bare record. */
    private fun decodeGroupAnswer(body: String): GroupInfo {
        val tree = runCatching { json.parseToJsonElement(body) }.getOrNull() as? JsonObject ?: return GroupInfo()
        val inner = tree["info"] as? JsonObject ?: tree
        return json.decodeFromJsonElement(GroupInfo.serializer(), inner)
    }

    // --- people ---

    suspend fun contacts(): Map<String, String> =
        decode(MapSerializer(String.serializer(), String.serializer()), http.get("/contacts"))

    suspend fun setContactName(pk: String, name: String) {
        http.post("/contacts", obj { put("pk", pk); put("name", name) })
    }

    suspend fun importContacts(names: Map<String, String>) {
        http.post("/contacts/import", obj { putJsonObject("names") { names.forEach { (k, v) -> put(k, v) } } })
    }

    suspend fun profile(): Profile = decode(Profile.serializer(), http.get("/profile"))

    suspend fun setProfile(name: String, avatarDataUrl: String?): Profile = decode(
        Profile.serializer(),
        http.post("/profile", obj { put("name", name); avatarDataUrl?.let { put("avatar", it) } }, slowPeer = true),
    )

    suspend fun clearProfile(): Profile = decode(Profile.serializer(), http.post("/profile", obj { put("clear", true) }))

    suspend fun peerProfile(pk: String): Profile =
        decode(Profile.serializer(), http.get("/profile/peer?pk=${enc(pk)}", slowPeer = true))

    suspend fun presence(pks: List<String>): PresenceAnswer = decode(
        PresenceAnswer.serializer(),
        http.post("/presence", obj { put("pks", buildJsonArray { pks.forEach { add(kotlinx.serialization.json.JsonPrimitive(it)) } }) }),
    )

    suspend fun privacy(): PrivacyState = decode(PrivacyState.serializer(), http.get("/privacy"))

    suspend fun setApproval(on: Boolean): PrivacyState =
        decode(PrivacyState.serializer(), http.post("/privacy", obj { put("approval", on) }))

    suspend fun accept(pk: String, accepted: Boolean = true): PrivacyState = decode(
        PrivacyState.serializer(),
        http.post("/privacy/accept", obj { put("pk", pk); if (!accepted) put("accepted", false) }),
    )

    suspend fun block(pk: String, blocked: Boolean): PrivacyState =
        decode(PrivacyState.serializer(), http.post("/privacy/block", obj { put("pk", pk); put("blocked", blocked) }))

    // --- pairing ---

    suspend fun pairs(): List<PairRecord> = decodeList(PairRecord.serializer(), http.get("/pair"))

    suspend fun pair(pk: String) {
        http.post("/pair", obj { put("peer_pk", pk) }, slowPeer = true)
    }

    suspend fun unpair(pk: String) {
        http.delete("/pair/$pk")
    }

    suspend fun pairInvites(): List<PairInvite> =
        decodeList(PairInvite.serializer(), http.get("/pair/invites"))

    suspend fun acceptInvite(pk: String) {
        http.post("/pair/invites/$pk/accept", slowPeer = true)
    }

    suspend fun declineInvite(pk: String) {
        http.post("/pair/invites/$pk/decline")
    }

    // --- calls ---

    suspend fun calls(): List<CallRecord> = decodeList(CallRecord.serializer(), http.get("/calls"))

    suspend fun voiceAvailable(): Boolean = runCatching { http.get("/voice/active"); true }.getOrDefault(false)

    suspend fun ringbackInfo(): RingbackInfo = decode(RingbackInfo.serializer(), http.get("/voice/my-ringback?info=1"))

    suspend fun setRingback(bytes: ByteArray, type: String) {
        http.put("/voice/my-ringback", bytes, type)
    }

    suspend fun clearRingback() {
        http.delete("/voice/my-ringback")
    }

    // --- archive ---

    suspend fun importArchive(body: ByteArray): String = http.postBytes("/import", body, "application/json")

    companion object {
        val json = Json { ignoreUnknownKeys = true; isLenient = true; coerceInputValues = true; explicitNulls = false }

        /** The body a reply travels in (index.html _encodeReply). */
        fun replyEnvelope(text: String, reply: ReplyRef): String = buildJsonObject {
            putJsonObject("skychat_reply") {
                put("to_sender", reply.sender)
                put("to_ts", reply.ts)
                put("to_preview", reply.preview.take(120))
                put("text", text)
            }
        }.toString()

        /** A forward carries the text and, out of a channel only, the channel's name. */
        fun forwardEnvelope(text: String, from: String?): String = buildJsonObject {
            putJsonObject("skychat_forward") {
                put("text", text)
                if (!from.isNullOrEmpty()) put("from", from.take(64))
            }
        }.toString()
    }
}
