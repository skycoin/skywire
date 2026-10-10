package com.skycoin.skywire.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** The visor's mail routes, `/api/visors/{pk}/mail` (pkg/visor/hypervisor_handlers_mail.go). */
object MailFolder {
    const val INBOX = "INBOX"
    const val SENT = "Sent"
}

@Serializable
data class MailLimits(
    @SerialName("max_message_size") val maxMessageSize: Long = 0,
    @SerialName("max_total_size") val maxTotalSize: Long = 0,
    /** Go's time.Duration, in nanoseconds. */
    @SerialName("max_age") val maxAgeNanos: Long = 0,
)

@Serializable
data class MailStatus(
    val enabled: Boolean = false,
    val running: Boolean = false,
    /** Why the mailbox is not running, when it is not. */
    val reason: String = "",
    val limits: MailLimits = MailLimits(),
    /** Bytes held by Inbox and Sent together. */
    val usage: Long = 0,
    val address: String = "",
    @SerialName("address_dmsg") val addressDmsg: String = "",
    /** Hex keys allowed to deliver; empty means everyone. */
    val whitelist: List<String> = emptyList(),
    val unread: Int = 0,
    val total: Int = 0,
)

@Serializable
data class MailSummary(
    val id: String,
    val folder: String = "",
    val from: String = "",
    val to: String = "",
    val subject: String = "",
    /** RFC 3339; Go's zero time when the message has no Date header. */
    val date: String = "",
    val seen: Boolean = false,
    val size: Long = 0,
    /** The sender key the transport authenticated (inbox only). */
    @SerialName("peer_pk") val peerPk: String = "",
    /** The From address names that key, so it cannot be forged. */
    @SerialName("from_verified") val fromVerified: Boolean = false,
)

@Serializable
data class MailAttachmentInfo(
    val name: String = "",
    @SerialName("content_type") val contentType: String = "",
    val size: Long = 0,
)

@Serializable
data class MailMessage(
    val from: String = "",
    val to: String = "",
    val cc: String = "",
    val subject: String = "",
    /** The Date header as sent. */
    val date: String = "",
    @SerialName("message_id") val messageId: String = "",
    @SerialName("peer_pk") val peerPk: String = "",
    @SerialName("from_verified") val fromVerified: Boolean = false,
    val text: String = "",
    /** The text was reduced from an HTML-only message. */
    @SerialName("from_html") val fromHtml: Boolean = false,
    val attachments: List<MailAttachmentInfo> = emptyList(),
)

@Serializable
data class MailOutgoingAttachment(
    val name: String,
    @SerialName("content_type") val contentType: String,
    /** Standard base64, as Go encodes a []byte. */
    val data: String,
)

@Serializable
data class MailOutgoing(
    val to: List<String>,
    val cc: List<String> = emptyList(),
    val subject: String = "",
    val body: String = "",
    @SerialName("in_reply_to") val inReplyTo: String = "",
    val attachments: List<MailOutgoingAttachment> = emptyList(),
)

@Serializable
data class MailRecipientResult(
    val rcpt: String = "",
    val error: String = "",
    /** skynet, dmsg, or local for this visor's own key. */
    val via: String = "",
)

@Serializable
data class MailSendResult(
    val id: String = "",
    @SerialName("message_id") val messageId: String = "",
    val recipients: List<MailRecipientResult> = emptyList(),
    /** Set when nothing was delivered. */
    val error: String = "",
) {
    val delivered: Int get() = recipients.count { it.error.isEmpty() }
}
