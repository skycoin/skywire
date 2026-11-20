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
import com.skycoin.skywire.chat.isOwnUpload
import com.skycoin.skywire.chat.isVoiceMessage
import com.skycoin.skywire.chat.isoMillis
import com.skycoin.skywire.chat.nanoText
import com.skycoin.skywire.chat.parseTime
import com.skycoin.skywire.chat.sameMessage
import com.skycoin.skywire.chat.thumbPath
import com.skycoin.skywire.chat.tickStatus
import com.skycoin.skywire.chat.withStored
import com.skycoin.skywire.chat.withoutFailedTwins
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
        val codes = listOf("banned", "removed", "muted", "channel", "read_only")
        val res = codes.map { cannotPostRes(it) }
        assertTrue(res.none { it == null })
        assertEquals(codes.size, res.toSet().size)
        assertNull(cannotPostRes(""))
        assertNull(cannotPostRes("something_newer"))
    }

    /**
     * A voice message whose send failed or lost its answer has no transfer id,
     * and its caption is not the text skychat stores. It showed twice once the
     * stored record was merged in.
     */
    @Test
    fun aStoredFileIsItsOwnBubbleNotASecondOne() {
        val clip = ChatFile(name = "voice-20261120-171651.oga", size = 12_493, status = "failed", localPath = "/cache/voice.oga")
        val bubble = ChatMessage(ts = 1_000_000, text = "\uD83C\uDFA4 Voice message", file = clip)
        val stored = ChatMessage(
            key = "h:b0d8", ts = 1_009_000, text = "\uD83D\uDCCE voice-20261120-171651.oga (12.2 KB)", wireId = "b0d8", status = "sent",
            file = ChatFile(id = "b0d8", name = clip.name, size = clip.size, status = "failed"),
        )
        assertTrue(sameMessage(bubble, stored))
        // The failed try keeps its Retry and gets no tick.
        val failed = withStored(bubble, stored)
        assertEquals("b0d8", failed.file!!.id)
        assertEquals("failed", failed.file!!.status)
        assertEquals("", failed.wireId)

        // The retry got another id: the failed try's record is still this bubble.
        val retried = bubble.copy(file = clip.copy(id = "c1e9", status = "sent"))
        assertTrue(sameMessage(retried, stored))
        assertEquals("c1e9", withStored(retried, stored).file!!.id)

        // The answer was lost but the file went out.
        val sent = stored.copy(file = stored.file!!.copy(status = "sent", url = "/files/b0d8.oga"))
        val healed = withStored(bubble, sent)
        assertEquals("sent", healed.file!!.status)
        assertEquals("/files/b0d8.oga", healed.file!!.url)
        assertEquals("b0d8", healed.wireId)
        assertEquals("sent", healed.status)

        // Two sends of one picked file both arrived: two messages.
        val first = ChatMessage(ts = 1_000_000, text = "a.pdf", file = ChatFile(id = "aa", name = "a.pdf", size = 9, status = "sent"))
        val second = ChatMessage(ts = 1_030_000, text = "a.pdf", file = ChatFile(id = "bb", name = "a.pdf", size = 9, status = "sent"))
        assertFalse(sameMessage(first, second))
        // Another recording, a peer's file of the same name, and a late record are not this bubble.
        assertFalse(sameMessage(bubble, stored.copy(file = stored.file!!.copy(name = "voice-20261120-171700.oga"))))
        assertFalse(sameMessage(bubble, stored.copy(from = "02ab")))
        assertFalse(sameMessage(bubble, stored.copy(ts = 1_000_000 + 121_000)))
        // Text is matched as before.
        assertTrue(sameMessage(ChatMessage(ts = 5, text = "hi"), ChatMessage(ts = 9, text = "hi", wireId = "x")))
        assertFalse(sameMessage(ChatMessage(ts = 5, text = "hi", wireId = "y"), ChatMessage(ts = 9, text = "hi", wireId = "x")))
    }

    /** group.go echoes an own file post; it can reach the app before `/send-file` answers with the id. */
    @Test
    fun anOwnGroupFileEchoIsKnownBeforeItsId() {
        val pending = ChatFile(name = "voice-20261120-134400.m4a", size = 4_608, status = "sending")
        assertTrue(isOwnUpload(pending, "f1", "voice-20261120-134400.m4a", 4_608))
        assertFalse(isOwnUpload(pending, "f1", "voice-20261120-134401.m4a", 4_608))
        assertFalse(isOwnUpload(pending, "", "voice-20261120-134400.m4a", 4_608))
        assertTrue(isOwnUpload(pending.copy(id = "f1"), "f1", "other", 1))
        assertFalse(isOwnUpload(pending.copy(id = "f2"), "f1", "voice-20261120-134400.m4a", 4_608))
        assertFalse(isOwnUpload(null, "f1", "x", 1))
    }

    /** 0.0.12 filed the stored record of a failed try as a second bubble; it goes when the thread is next merged. */
    @Test
    fun aFailedTwinFromAnOlderVersionIsDropped() {
        val clip = ChatFile(id = "c1e9", name = "voice-20261120-171830.oga", size = 12_493, status = "sent")
        val bubble = ChatMessage(key = "k1", ts = 1_000_000, text = "\uD83C\uDFA4 Voice message", file = clip)
        val twin = ChatMessage(key = "h:b0d8", ts = 1_009_000, text = "\uD83D\uDCCE voice (12.2 KB)", wireId = "b0d8", file = clip.copy(id = "b0d8", status = "failed"))
        val alone = ChatMessage(key = "h:77aa", ts = 2_000_000, wireId = "77aa", file = ChatFile(id = "77aa", name = "sent-from-the-cli.pdf", size = 5, status = "failed"))
        val theirs = twin.copy(key = "h:9f", from = "02ab")
        val list = listOf(bubble, twin, alone, theirs)
        assertEquals(listOf(bubble, alone, theirs), withoutFailedTwins(list))
        val clean = listOf(bubble, alone)
        assertTrue(withoutFailedTwins(clean) === clean)
    }
}
