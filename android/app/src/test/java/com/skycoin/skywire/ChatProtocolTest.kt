package com.skycoin.skywire

import com.skycoin.skywire.chat.ChatFile
import com.skycoin.skywire.chat.ChatMessage
import com.skycoin.skywire.chat.GroupInfo
import com.skycoin.skywire.chat.ReplyRef
import com.skycoin.skywire.chat.SkychatClient
import com.skycoin.skywire.chat.SseDispatch
import com.skycoin.skywire.chat.SseMessage
import com.skycoin.skywire.chat.humanSize
import com.skycoin.skywire.chat.isSquareVideoMessage
import com.skycoin.skywire.chat.isVideoMessage
import com.skycoin.skywire.chat.isVoiceMessage
import com.skycoin.skywire.chat.isoMillis
import com.skycoin.skywire.chat.nanoText
import com.skycoin.skywire.chat.parseTime
import com.skycoin.skywire.chat.thumbPath
import com.skycoin.skywire.chat.tickStatus
import com.skycoin.skywire.ui.chat.cannotPostRes
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The wire details the native chat shares with skychat and its web UI: a
 * mismatch here shows up as a reply or forward that renders as raw JSON on
 * the other side, or a clip the desktop plays with the wrong player.
 */
class ChatProtocolTest {

    private val json = SkychatClient.json

    @Test
    fun replyEnvelopeHasTheKeysReplyGoParses() {
        val body = SkychatClient.replyEnvelope("yes", ReplyRef("02ab", "2026-10-28T05:25:00.000Z", "x".repeat(200)))
        val inner = json.parseToJsonElement(body).jsonObject["skychat_reply"]!!.jsonObject
        assertEquals(setOf("to_sender", "to_ts", "to_preview", "text"), inner.keys)
        assertEquals(120, inner["to_preview"]!!.jsonPrimitive.content.length)
        assertEquals("yes", inner["text"]!!.jsonPrimitive.content)
    }

    /** forward.go: the envelope has no author field by construction; only a channel may be named. */
    @Test
    fun forwardEnvelopeNeverNamesAnAuthor() {
        val plain = json.parseToJsonElement(SkychatClient.forwardEnvelope("hi", null)).jsonObject["skychat_forward"] as JsonObject
        assertEquals(setOf("text"), plain.keys)
        val channel = json.parseToJsonElement(SkychatClient.forwardEnvelope("hi", "N".repeat(80))).jsonObject["skychat_forward"] as JsonObject
        assertEquals(setOf("text", "from"), channel.keys)
        assertEquals(64, channel["from"]!!.jsonPrimitive.content.length)
    }

    /** Pair (CXO) messages arrive with the envelope still on. */
    @Test
    fun pairBodiesAreUnwrappedHere() {
        val reply = SseDispatch.unwrap(SkychatClient.replyEnvelope("inner", ReplyRef("02AB", "t", "p")))
        assertEquals("inner", reply.text)
        assertEquals("02ab", reply.reply?.sender)
        val fwd = SseDispatch.unwrap(SkychatClient.forwardEnvelope("passed on", "News"))
        assertEquals("passed on", fwd.text)
        assertTrue(fwd.forwarded)
        assertEquals("News", fwd.forwardFrom)
        val plain = SseDispatch.unwrap("{not json")
        assertEquals("{not json", plain.text)
        assertNull(plain.reply)
    }

    @Test
    fun statusesOnlyMoveForward() {
        assertEquals("read", SseDispatch.advance("received", "read"))
        assertEquals("read", SseDispatch.advance("read", "sent"))
        assertEquals("received", SseDispatch.advance("sent", "received"))
        assertEquals("failed", SseDispatch.advance("pending", "failed"))
        assertEquals("sent", SseDispatch.advance("sent", "failed"))
        assertEquals("pending", SseDispatch.advance("failed", "pending"))
    }

    @Test
    fun fileTicksFollowTheTransfer() {
        fun mine(status: String, msg: String = "") = ChatMessage(file = ChatFile(name = "a.png", status = status), status = msg)
        assertEquals("pending", tickStatus(mine("sending"), group = false))
        assertEquals("received", tickStatus(mine("sent"), group = false))
        assertEquals("sent", tickStatus(mine("sent"), group = true))
        assertEquals("sent", tickStatus(mine("stored"), group = false))
        assertEquals("read", tickStatus(mine("sent", "read"), group = false))
        assertEquals("", tickStatus(ChatMessage(from = "02ab"), group = false))
    }

    /** index.html isVoiceMessage / isVideoMessage: the name prefix is the only signal. */
    @Test
    fun recordedClipsAreKnownByName() {
        assertTrue(ChatFile(name = "voice-20261028-132900.oga").isVoiceMessage())
        assertTrue(ChatFile(name = "voice-20261028-132900.m4a").isVoiceMessage())
        assertFalse(ChatFile(name = "voice-memo.oga").isVoiceMessage())
        assertTrue(ChatFile(name = "video-20261028-133902.mp4").isVideoMessage())
        assertTrue(ChatFile(name = "video-sq-20261028-133902.mp4").isSquareVideoMessage())
        assertFalse(ChatFile(name = "video-20261028-133902.mp4").isSquareVideoMessage())
        assertFalse(ChatFile(name = "holiday.mp4").isVideoMessage())
    }

    @Test
    fun thumbnailsLiveBesideServedFiles() {
        assertEquals("/thumb/ab.png", ChatFile(name = "ab.png", url = "/files/ab.png").thumbPath())
        assertNull(ChatFile(name = "ab.png").thumbPath())
    }

    @Test
    fun sizesUseTheWebFormat() {
        assertEquals("512 B", humanSize(512))
        assertEquals("9.4 KB", humanSize(9_626))
        assertEquals("2.0 MB", humanSize(2L * 1024 * 1024))
    }

    /** ts_nano is an int64 that a double would round; it must come through exactly. */
    @Test
    fun feedIdsSurviveExactly() {
        val m = json.decodeFromString(SseMessage.serializer(), """{"channel":"group","ts_nano":1793167170842123457}""")
        assertEquals("1793167170842123457", m.tsNano.nanoText())
        val seed = json.decodeFromString(SseMessage.serializer(), """{"sender":"self","history":"true"}""")
        assertTrue(seed.history != null)
    }

    @Test
    fun timesParseWithNanosecondsAndPrintLikeJavaScript() {
        val ms = parseTime("2026-10-28T09:41:07.123456789Z")!!
        assertEquals("2026-10-28T09:41:07.123Z", isoMillis(ms))
        assertEquals(parseTime("2026-10-28T17:41:07.123+08:00"), ms)
        assertNull(parseTime(""))
    }

    /** group-delete names an author: another member's message with the same feed id stays. */
    @Test
    fun groupTombstoneRemovesOnlyItsAuthorsMessage() {
        val me = "03aa"
        val list = listOf(
            ChatMessage(key = "a", from = "02bb", tsNano = "100"),
            ChatMessage(key = "b", from = "02cc", tsNano = "100"),
            ChatMessage(key = "c", from = ChatMessage.ME, tsNano = "200"),
        )
        assertEquals(listOf("b", "c"), SseDispatch.withoutDeleted(list, "100", "02bb", me).map { it.key })
        assertEquals(listOf("a", "b"), SseDispatch.withoutDeleted(list, "200", me, me).map { it.key })
        assertTrue(SseDispatch.withoutDeleted(list, "300", "02bb", me) === list)
    }

    @Test
    fun everyPostBlockCodeHasItsOwnString() {
        val info = json.decodeFromString<GroupInfo>("""{"can_post":false,"cannot_post_reason":"this is a channel — only admins can post","cannot_post_code":"channel"}""")
        assertEquals("channel", info.cannotPostCode)
        // The codes pkg/skychat/group PostBlock returns.
        val codes = listOf("banned", "muted", "channel", "read_only")
        val res = codes.map { cannotPostRes(it) }
        assertTrue(res.none { it == null })
        assertEquals(codes.size, res.toSet().size)
        assertNull(cannotPostRes(""))
        assertNull(cannotPostRes("something_newer"))
    }
}
