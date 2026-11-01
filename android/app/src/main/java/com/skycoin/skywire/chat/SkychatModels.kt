package com.skycoin.skywire.chat

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

/** `GET /status`; only the key matters to the UI. */
@Serializable
data class SkychatStatus(
    @SerialName("visor_pk") val visorPk: String = "",
    @SerialName("persistence_enabled") val persistence: Boolean = false,
    @SerialName("pairing_enabled") val pairing: Boolean = false,
)

/** One stored DM, from `GET /history`. */
@Serializable
data class HistoryRecord(
    val id: String = "",
    val peer: String = "",
    val from: String = "",
    val outgoing: Boolean = false,
    val text: String = "",
    val timestamp: String = "",
    @SerialName("file_id") val fileId: String = "",
    @SerialName("file_name") val fileName: String = "",
    @SerialName("file_size") val fileSize: Long = 0,
    @SerialName("file_status") val fileStatus: String = "",
    @SerialName("file_url") val fileUrl: String = "",
    @SerialName("reply_to_ts") val replyToTs: String = "",
    @SerialName("reply_to_preview") val replyToPreview: String = "",
    @SerialName("reply_to_sender") val replyToSender: String = "",
    val forwarded: Boolean = false,
    @SerialName("forward_from") val forwardFrom: String = "",
)

/** One group feed row, from `GET /group/<id>/history`. */
@Serializable
data class GroupHistoryRecord(
    val ts: String = "",
    @SerialName("ts_nano") val tsNano: JsonElement? = null,
    @SerialName("sender_pk") val senderPk: String = "",
    val text: String = "",
    @SerialName("file_id") val fileId: String = "",
    @SerialName("file_name") val fileName: String = "",
    @SerialName("file_size") val fileSize: Long = 0,
    @SerialName("file_url") val fileUrl: String = "",
    @SerialName("reply_to_ts") val replyToTs: String = "",
    @SerialName("reply_to_preview") val replyToPreview: String = "",
    @SerialName("reply_to_sender") val replyToSender: String = "",
    val forwarded: Boolean = false,
    @SerialName("forward_from") val forwardFrom: String = "",
)

/** The group record `GET /group` lists and every group action answers with. */
@Serializable
data class GroupInfo(
    val id: String = "",
    val name: String = "",
    val kind: String = "",
    val mode: String = "",
    val role: String = "",
    @SerialName("owner_pk") val ownerPk: String = "",
    val admins: List<String> = emptyList(),
    val members: List<String> = emptyList(),
    val muted: List<String> = emptyList(),
    val banned: List<String> = emptyList(),
    @SerialName("join_policy") val joinPolicy: String = "",
    val policy: String = "",
    val status: String = "",
    @SerialName("read_only") val readOnly: Boolean = false,
    @SerialName("can_post") val canPost: Boolean? = null,
    @SerialName("cannot_post_reason") val cannotPostReason: String = "",
    @SerialName("cannot_post_code") val cannotPostCode: String = "",
    @SerialName("pending_joins") val pendingJoins: Int = 0,
    val avatar: String = "",
    val listed: Boolean = false,
    @SerialName("peer_backfill") val peerBackfill: Boolean? = null,
    @SerialName("join_pow_bits") val joinPowBits: Int? = null,
    @SerialName("key_epoch") val keyEpoch: Long = 0,
    @SerialName("key_age_seconds") val keyAgeSeconds: Long = 0,
    @SerialName("key_rotates_in_seconds") val keyRotatesInSeconds: Long? = null,
    @SerialName("governance_sealed") val governanceSealed: Boolean = false,
) {
    val isChannel: Boolean get() = kind == "channel"
    val encrypted: Boolean get() = kind == "private" || mode == "private"
    val approval: Boolean get() = joinPolicy == "approval" || policy == "approval"
    val postable: Boolean get() = canPost != false
}

@Serializable
data class GroupCreated(val invite: String = "", val info: GroupInfo = GroupInfo())

@Serializable
data class GroupInvite(val invite: String = "")

@Serializable
data class JoinRequest(val pk: String = "", val note: String = "")

/** `GET /group/resolve`: what an address or invite names. */
@Serializable
data class Resolved(
    val target: String = "",
    val pk: String = "",
    val address: String = "",
    val invite: String = "",
    val joined: Boolean = false,
    val status: String = "",
    val group: ResolvedGroup? = null,
)

@Serializable
data class ResolvedGroup(
    val id: String = "",
    val name: String = "",
    val kind: String = "",
    val policy: String = "",
    val mode: String = "",
    val banned: Boolean = false,
    @SerialName("host_pk") val hostPk: String = "",
    @SerialName("price_hint") val priceHint: String = "",
)

@Serializable
data class Catalog(val entries: List<CatalogEntry> = emptyList(), val truncated: Boolean = false)

@Serializable
data class CatalogEntry(
    val id: String = "",
    val name: String = "",
    val kind: String = "",
    val policy: String = "",
    @SerialName("price_hint") val priceHint: String = "",
    val address: String = "",
)

@Serializable
data class Profile(
    val pk: String = "",
    val name: String = "",
    val avatar: String = "",
    val address: String = "",
)

@Serializable
data class PrivacyState(
    val approval: Boolean = false,
    val blocked: List<String> = emptyList(),
    val requests: List<String> = emptyList(),
)

@Serializable
data class PresencePeer(val online: Boolean = false, @SerialName("rtt_ms") val rttMs: Long = 0, val at: Long = 0)

@Serializable
data class PresenceAnswer(val peers: Map<String, PresencePeer> = emptyMap())

@Serializable
data class PairRecord(
    @SerialName("peer_pk") val peerPk: String = "",
    val status: String = "",
    @SerialName("established_at") val establishedAt: String = "",
    val epoch: String = "",
    @SerialName("forward_secret") val forwardSecret: Boolean = false,
    @SerialName("key_generation") val keyGeneration: Long = 0,
)

@Serializable
data class PairInvite(@SerialName("peer_pk") val peerPk: String = "", @SerialName("received_at") val receivedAt: String = "")

@Serializable
data class SearchHit(
    val peer: String = "",
    @SerialName("group_id") val groupId: String = "",
    val from: String = "",
    val outgoing: Boolean = false,
    val text: String = "",
    val ts: String = "",
)

@Serializable
data class CallRecord(
    val peer: String = "",
    val direction: String = "",
    val outcome: String = "",
    val at: String = "",
    val seconds: Long = 0,
)

@Serializable
data class LinkState(val ready: Boolean = false, val connecting: Boolean = false, val error: String = "")

@Serializable
data class SendAnswer(val ok: Boolean = true, val id: String = "")

@Serializable
data class FileSent(val id: String = "", @SerialName("file_url") val fileUrl: String = "")

@Serializable
data class RingbackInfo(val set: Boolean = false, val size: Long = 0, val type: String = "")

@Serializable
data class CallStarted(@SerialName("call_id") val callId: String = "")

/** `POST /import`'s report; every count is optional. */
@Serializable
data class ImportReport(
    val error: String = "",
    val messages: Int = 0,
    @SerialName("group_messages") val groupMessages: Int = 0,
    val contacts: Int = 0,
    val duplicates: Int = 0,
    val persistence: Boolean? = null,
    val evicted: Int = 0,
    val expiring: Int = 0,
    val rejected: Int = 0,
)

/** Every `/sse` payload is one flat object; the fields present depend on `channel`. */
@Serializable
data class SseMessage(
    val channel: String = "",
    /** Present (as the string "true") on the replay seed every connect starts with. */
    val history: JsonElement? = null,
    val dir: String = "",
    val sender: String = "",
    val to: String = "",
    val network: String = "",
    val message: String = "",
    val id: String = "",
    val request: Boolean = false,
    val peer: String = "",
    val event: String = "",
    val status: String = "",
    @SerialName("group_id") val groupId: String = "",
    @SerialName("group_name") val groupName: String = "",
    val ts: String = "",
    @SerialName("ts_nano") val tsNano: JsonElement? = null,
    @SerialName("deleted_ts_nano") val deletedTsNano: JsonElement? = null,
    @SerialName("deleted_by") val deletedBy: String = "",
    @SerialName("file_id") val fileId: String = "",
    @SerialName("file_name") val fileName: String = "",
    @SerialName("file_size") val fileSize: Long = 0,
    @SerialName("file_status") val fileStatus: String = "",
    @SerialName("file_url") val fileUrl: String = "",
    @SerialName("reply_to_ts") val replyToTs: String = "",
    @SerialName("reply_to_preview") val replyToPreview: String = "",
    @SerialName("reply_to_sender") val replyToSender: String = "",
    val forwarded: Boolean = false,
    @SerialName("forward_from") val forwardFrom: String = "",
)

/** `ts_nano` arrives as a JSON int64; keep it as its exact decimal text. */
fun JsonElement?.nanoText(): String =
    (this as? kotlinx.serialization.json.JsonPrimitive)?.content?.takeIf { it != "null" && it != "0" }.orEmpty()
