package com.skycoin.skywire.chat

import kotlinx.serialization.Serializable
import java.time.Instant
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter
import java.util.UUID

/** Thread keys: a DM is the peer key, a group its id, and the notes thread this. */
const val SAVED_KEY = "saved"

private val PK = Regex("^[0-9a-f]{66}$")

fun isPk(s: String): Boolean = PK.matches(s)

/** The key a thread is filed under; [SAVED_KEY] is neither a pk nor a group id. */
fun isDmKey(key: String): Boolean = isPk(key)

@Serializable
data class ChatFile(
    val id: String = "",
    val name: String = "file",
    val size: Long = 0,
    /** `sending`, `sent`, `stored`, `failed`, `received` or the server's `file_status`. */
    val status: String = "",
    /** A served `/files/…` path, or empty when this visor does not hold the bytes. */
    val url: String = "",
    /** The sender (inbound only), who a re-request goes to. */
    val from: String = "",
    /** Our own upload's local copy, kept for retry and instant preview. */
    val localPath: String = "",
)

@Serializable
data class ReplyRef(val sender: String = "", val ts: String = "", val preview: String = "")

/**
 * One message as the UI holds it: the shape of the web page's message
 * object, with times as epoch ms and a stable [key] in place of its `mid`.
 */
@Serializable
data class ChatMessage(
    val key: String = UUID.randomUUID().toString(),
    val ts: Long = System.currentTimeMillis(),
    /** `me` for our own, else the author's key. */
    val from: String = ME,
    val text: String = "",
    val viaPair: Boolean = false,
    val file: ChatFile? = null,
    val reply: ReplyRef? = null,
    val forwarded: Boolean = false,
    val forwardFrom: String = "",
    /** The id both visors know a DM by. */
    val wireId: String = "",
    /** Own messages: queued, pending, sent, received, read, failed. */
    val status: String = "",
    /** Group feed timestamp, the exact id of a group message. */
    val tsNano: String = "",
    val readSent: Boolean = false,
    /** Group or DM request flag the server attached. */
    val request: Boolean = false,
) {
    val mine: Boolean get() = from == ME

    /** Id a DM delete or receipt names this message by. */
    val dmWireId: String get() = wireId.ifEmpty { file?.id.orEmpty() }

    companion object {
        const val ME = "me"
    }
}

private val ISO = DateTimeFormatter.ofPattern("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'").withZone(ZoneOffset.UTC)

/** The ISO form JavaScript's `toISOString()` writes, which reply envelopes carry. */
fun isoMillis(ms: Long): String = ISO.format(Instant.ofEpochMilli(ms))

/** Parse an RFC 3339 / ISO time from the server; null when it is not one. */
fun parseTime(s: String): Long? {
    if (s.isBlank()) return null
    return runCatching { java.time.OffsetDateTime.parse(s).toInstant().toEpochMilli() }.getOrNull()
        ?: runCatching { Instant.parse(s).toEpochMilli() }.getOrNull()
}

/** Delivery order; `failed` sits outside it. */
fun statusRank(status: String): Int = when (status) {
    "queued" -> 0
    "pending" -> 1
    "sent" -> 2
    "received" -> 3
    "read" -> 4
    else -> -1
}

/** What a tick shows for [m], including the file-transfer states (index.html _msgTickStatus). */
fun tickStatus(m: ChatMessage, group: Boolean): String {
    if (!m.mine) return ""
    val f = m.file ?: return m.status.ifEmpty { "sent" }
    if (m.status == "read") return "read"
    return when (f.status) {
        "sending" -> "pending"
        "failed" -> "failed"
        "received" -> "received"
        "stored" -> "sent"
        else -> if (group) "sent" else "received"
    }
}

private val IMAGE = Regex("\\.(png|jpe?g|gif|webp|bmp|svg|avif|heic)$", RegexOption.IGNORE_CASE)
private val VIDEO = Regex("\\.(mp4|m4v|webm|ogv|mov|mkv|3gp)$", RegexOption.IGNORE_CASE)
private val AUDIO = Regex("\\.(mp3|ogg|oga|opus|wav|m4a|aac|flac|weba)$", RegexOption.IGNORE_CASE)

fun ChatFile.isImage(): Boolean = IMAGE.containsMatchIn(name)
fun ChatFile.isVideo(): Boolean = VIDEO.containsMatchIn(name)
fun ChatFile.isAudio(): Boolean = AUDIO.containsMatchIn(name)

/** A clip recorded in a composer: the `voice-<digit>` / `video-<digit>` name prefix is the only signal. */
fun ChatFile.isVoiceMessage(): Boolean = Regex("^voice-\\d", RegexOption.IGNORE_CASE).containsMatchIn(name) && isAudio()
fun ChatFile.isVideoMessage(): Boolean = Regex("^video-(sq-)?\\d", RegexOption.IGNORE_CASE).containsMatchIn(name) && isVideo()
fun ChatFile.isSquareVideoMessage(): Boolean = Regex("^video-sq-\\d", RegexOption.IGNORE_CASE).containsMatchIn(name) && isVideo()

/** `/thumb/<rest>` for a served `/files/<rest>`, or null. */
fun ChatFile.thumbPath(): String? = url.takeIf { it.startsWith("/files/") }?.let { "/thumb/" + it.removePrefix("/files/") }

fun humanSize(n: Long): String {
    if (n < 1024) return "$n B"
    val units = arrayOf("KB", "MB", "GB", "TB")
    var v = n / 1024.0
    var i = 0
    while (v >= 1024 && i < units.size - 1) {
        v /= 1024
        i++
    }
    return String.format(java.util.Locale.US, "%.1f %s", v, units[i])
}

/** `abcdef12…wxyz`, the short form of a key every list shows. */
fun abbrevPk(pk: String): String = if (pk.length >= 14) pk.take(8) + "…" + pk.takeLast(4) else pk

/** One-line preview of a message for quotes and the touch sheet (≤120 chars). */
fun messagePreview(m: ChatMessage): String {
    m.file?.let { return it.name.ifEmpty { "Attachment" } }
    return m.text.replace(Regex("\\s+"), " ").trim().take(120)
}
