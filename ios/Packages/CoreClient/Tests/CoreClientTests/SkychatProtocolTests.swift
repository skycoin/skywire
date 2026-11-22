@testable import CoreClient
import XCTest

/// The wire details the native chat shares with skychat and its web UI
/// (Android: ChatProtocolTest.kt, the same cases).
final class SkychatProtocolTests: XCTestCase {
    private func object(_ json: String, _ key: String) throws -> [String: Any] {
        let outer = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any])
        return try XCTUnwrap(outer[key] as? [String: Any])
    }

    func testReplyEnvelopeHasTheKeysReplyGoParses() throws {
        let body = Skychat.replyEnvelope("yes", ReplyRef(sender: "02ab", ts: "2026-10-28T05:25:00.000Z", preview: String(repeating: "x", count: 200)))
        let inner = try object(body, "skychat_reply")
        XCTAssertEqual(Set(inner.keys), ["to_sender", "to_ts", "to_preview", "text"])
        XCTAssertEqual((inner["to_preview"] as? String)?.count, 120)
        XCTAssertEqual(inner["text"] as? String, "yes")
    }

    /// forward.go: the envelope has no author field by construction; only a channel may be named.
    func testForwardEnvelopeNeverNamesAnAuthor() throws {
        XCTAssertEqual(Set(try object(Skychat.forwardEnvelope("hi", from: nil), "skychat_forward").keys), ["text"])
        let channel = try object(Skychat.forwardEnvelope("hi", from: String(repeating: "N", count: 80)), "skychat_forward")
        XCTAssertEqual(Set(channel.keys), ["text", "from"])
        XCTAssertEqual((channel["from"] as? String)?.count, 64)
    }

    /// Pair (CXO) messages arrive with the envelope still on.
    func testPairBodiesAreUnwrappedHere() {
        let reply = Skychat.unwrap(Skychat.replyEnvelope("inner", ReplyRef(sender: "02AB", ts: "t", preview: "p")))
        XCTAssertEqual(reply.text, "inner")
        XCTAssertEqual(reply.reply?.sender, "02ab")
        let forward = Skychat.unwrap(Skychat.forwardEnvelope("passed on", from: "News"))
        XCTAssertEqual(forward.text, "passed on")
        XCTAssertTrue(forward.forwarded)
        XCTAssertEqual(forward.forwardFrom, "News")
        let plain = Skychat.unwrap("{not json")
        XCTAssertEqual(plain.text, "{not json")
        XCTAssertNil(plain.reply)
    }

    func testStatusesOnlyMoveForward() {
        XCTAssertEqual(Skychat.advance("received", "read"), "read")
        XCTAssertEqual(Skychat.advance("read", "sent"), "read")
        XCTAssertEqual(Skychat.advance("sent", "received"), "received")
        XCTAssertEqual(Skychat.advance("pending", "failed"), "failed")
        XCTAssertEqual(Skychat.advance("sent", "failed"), "sent")
        XCTAssertEqual(Skychat.advance("failed", "pending"), "pending")
    }

    func testFileTicksFollowTheTransfer() {
        func mine(_ status: String, _ msg: String = "") -> ChatMessage {
            ChatMessage(file: ChatFile(name: "a.png", status: status), status: msg)
        }
        XCTAssertEqual(Skychat.tickStatus(mine("sending"), group: false), "pending")
        XCTAssertEqual(Skychat.tickStatus(mine("sent"), group: false), "received")
        XCTAssertEqual(Skychat.tickStatus(mine("sent"), group: true), "sent")
        XCTAssertEqual(Skychat.tickStatus(mine("stored"), group: false), "sent")
        XCTAssertEqual(Skychat.tickStatus(mine("sent", "read"), group: false), "read")
        XCTAssertEqual(Skychat.tickStatus(ChatMessage(from: "02ab"), group: false), "")
    }

    /// index.html isVoiceMessage / isVideoMessage: the name prefix is the only signal.
    func testRecordedClipsAreKnownByName() {
        XCTAssertTrue(ChatFile(name: "voice-20261028-132900.oga").isVoiceMessage)
        XCTAssertTrue(ChatFile(name: "voice-20261028-132900.m4a").isVoiceMessage)
        XCTAssertFalse(ChatFile(name: "voice-memo.oga").isVoiceMessage)
        XCTAssertTrue(ChatFile(name: "video-20261028-133902.mp4").isVideoMessage)
        XCTAssertTrue(ChatFile(name: "video-sq-20261028-133902.mp4").isSquareVideoMessage)
        XCTAssertFalse(ChatFile(name: "video-20261028-133902.mp4").isSquareVideoMessage)
        XCTAssertFalse(ChatFile(name: "holiday.mp4").isVideoMessage)
    }

    func testThumbnailsLiveBesideServedFiles() {
        XCTAssertEqual(ChatFile(name: "ab.png", url: "/files/ab.png").thumbPath, "/thumb/ab.png")
        XCTAssertNil(ChatFile(name: "ab.png").thumbPath)
    }

    func testSizesUseTheWebFormat() {
        XCTAssertEqual(Skychat.humanSize(512), "512 B")
        XCTAssertEqual(Skychat.humanSize(9_626), "9.4 KB")
        XCTAssertEqual(Skychat.humanSize(2 * 1024 * 1024), "2.0 MB")
    }

    /// ts_nano is an int64 that a double would round; it must come through exactly.
    func testFeedIdsSurviveExactly() throws {
        let message = try XCTUnwrap(SseMessage(json: #"{"channel":"group","ts_nano":1793167170842123457}"#))
        XCTAssertEqual(message.tsNano, "1793167170842123457")
        XCTAssertFalse(message.history)
        let seed = try XCTUnwrap(SseMessage(json: #"{"sender":"self","history":"true"}"#))
        XCTAssertTrue(seed.history)
    }

    func testTimesParseWithNanosecondsAndPrintLikeJavaScript() throws {
        let ms = try XCTUnwrap(Skychat.parseTime("2026-10-28T09:41:07.123456789Z"))
        XCTAssertEqual(Skychat.isoMillis(ms), "2026-10-28T09:41:07.123Z")
        XCTAssertEqual(Skychat.parseTime("2026-10-28T17:41:07.123+08:00"), ms)
        XCTAssertEqual(Skychat.parseTime("2026-10-28T09:41:07Z"), ms - 123)
        XCTAssertNil(Skychat.parseTime(""))
    }

    /// group-delete names an author: another member's message with the same feed id stays.
    func testGroupTombstoneRemovesOnlyItsAuthorsMessage() {
        let me = "03aa"
        let list = [
            ChatMessage(key: "a", from: "02bb", tsNano: "100"),
            ChatMessage(key: "b", from: "02cc", tsNano: "100"),
            ChatMessage(key: "c", from: Skychat.me, tsNano: "200"),
        ]
        XCTAssertEqual(Skychat.withoutDeleted(list, nano: "100", by: "02bb", me: me).map(\.key), ["b", "c"])
        XCTAssertEqual(Skychat.withoutDeleted(list, nano: "200", by: me, me: me).map(\.key), ["a", "b"])
        XCTAssertEqual(Skychat.withoutDeleted(list, nano: "300", by: "02bb", me: me).count, 3)
    }

    func testKeysAreNormalised() {
        let pk = "02" + String(repeating: "ab", count: 32)
        XCTAssertEqual(Skychat.keyFrom(" skychat://\(pk.uppercased())/ "), pk)
        XCTAssertEqual(Skychat.keyFrom("skychat://\(pk)/group"), "")
        XCTAssertEqual(Skychat.abbrevPk(pk), "02ababab…abab")
        XCTAssertTrue(Skychat.isGroupKey("g1"))
        XCTAssertFalse(Skychat.isGroupKey(Skychat.savedKey))
        XCTAssertFalse(Skychat.isGroupKey(pk))
    }

    /// The local store reads files an older build wrote, missing fields and all.
    func testStoredMessagesDecodeWithMissingFields() throws {
        let decoded = try JSONDecoder().decode(ChatMessage.self, from: Data(#"{"key":"k","ts":5,"text":"hi","file":{"name":"a.png"}}"#.utf8))
        XCTAssertEqual(decoded.key, "k")
        XCTAssertTrue(decoded.mine)
        XCTAssertEqual(decoded.file?.name, "a.png")
        XCTAssertEqual(decoded.status, "")
    }

    /// A voice message whose send failed or lost its answer has no transfer id, and its caption is
    /// not the text skychat stores. It showed twice once the stored record was merged in.
    func testAStoredFileIsItsOwnBubbleNotASecondOne() throws {
        let clip = ChatFile(name: "voice-20261120-171651.oga", size: 12_493, status: "failed", localPath: "voice.oga")
        let bubble = ChatMessage(ts: 1_000_000, text: "🎤 Voice message", file: clip)
        var storedFile = clip
        storedFile.id = "b0d8"
        storedFile.localPath = ""
        let stored = ChatMessage(key: "h:b0d8", ts: 1_009_000, text: "📎 voice-20261120-171651.oga (12.2 KB)", file: storedFile, wireId: "b0d8", status: "sent")
        XCTAssertTrue(Skychat.sameMessage(bubble, stored))
        // The failed try keeps its Retry and gets no tick.
        let failed = Skychat.withStored(bubble, stored)
        XCTAssertEqual(failed.file?.id, "b0d8")
        XCTAssertEqual(failed.file?.status, "failed")
        XCTAssertEqual(failed.wireId, "")

        // The retry got another id: the failed try's record is still this bubble.
        var retried = bubble
        retried.file?.id = "c1e9"
        retried.file?.status = "sent"
        XCTAssertTrue(Skychat.sameMessage(retried, stored))
        XCTAssertEqual(Skychat.withStored(retried, stored).file?.id, "c1e9")

        // The answer was lost but the file went out.
        var sent = stored
        sent.file?.status = "sent"
        sent.file?.url = "/files/b0d8.oga"
        let healed = Skychat.withStored(bubble, sent)
        XCTAssertEqual(healed.file?.status, "sent")
        XCTAssertEqual(healed.file?.url, "/files/b0d8.oga")
        XCTAssertEqual(healed.wireId, "b0d8")
        XCTAssertEqual(healed.status, "sent")

        // Two sends of one picked file both arrived: two messages.
        let first = ChatMessage(ts: 1_000_000, text: "a.pdf", file: ChatFile(id: "aa", name: "a.pdf", size: 9, status: "sent"))
        let second = ChatMessage(ts: 1_030_000, text: "a.pdf", file: ChatFile(id: "bb", name: "a.pdf", size: 9, status: "sent"))
        XCTAssertFalse(Skychat.sameMessage(first, second))
        // Another recording, a peer's file of the same name, and a late record are not this bubble.
        var other = stored
        other.file?.name = "voice-20261120-171700.oga"
        XCTAssertFalse(Skychat.sameMessage(bubble, other))
        var theirs = stored
        theirs.from = "02ab"
        XCTAssertFalse(Skychat.sameMessage(bubble, theirs))
        var late = stored
        late.ts = 1_000_000 + 121_000
        XCTAssertFalse(Skychat.sameMessage(bubble, late))
        // Text is matched as before.
        XCTAssertTrue(Skychat.sameMessage(ChatMessage(ts: 5, text: "hi"), ChatMessage(ts: 9, text: "hi", wireId: "x")))
        XCTAssertFalse(Skychat.sameMessage(ChatMessage(ts: 5, text: "hi", wireId: "y"), ChatMessage(ts: 9, text: "hi", wireId: "x")))
    }

    /// group.go echoes an own file post; it can reach the app before `/send-file` answers with the id.
    func testAnOwnGroupFileEchoIsKnownBeforeItsId() {
        var pending = ChatFile(name: "voice-20261120-134400.m4a", size: 4_608, status: "sending")
        XCTAssertTrue(Skychat.isOwnUpload(pending, fileId: "f1", fileName: "voice-20261120-134400.m4a", fileSize: 4_608))
        XCTAssertFalse(Skychat.isOwnUpload(pending, fileId: "f1", fileName: "voice-20261120-134401.m4a", fileSize: 4_608))
        XCTAssertFalse(Skychat.isOwnUpload(pending, fileId: "", fileName: "voice-20261120-134400.m4a", fileSize: 4_608))
        pending.id = "f1"
        XCTAssertTrue(Skychat.isOwnUpload(pending, fileId: "f1", fileName: "other", fileSize: 1))
        pending.id = "f2"
        XCTAssertFalse(Skychat.isOwnUpload(pending, fileId: "f1", fileName: "voice-20261120-134400.m4a", fileSize: 4_608))
        XCTAssertFalse(Skychat.isOwnUpload(nil, fileId: "f1", fileName: "x", fileSize: 1))
    }

    /// An earlier build filed the stored record of a failed try as a second bubble; it goes at the next merge.
    func testAFailedTwinFromAnEarlierBuildIsDropped() {
        let clip = ChatFile(id: "c1e9", name: "voice-20261120-171830.oga", size: 12_493, status: "sent")
        let bubble = ChatMessage(key: "k1", ts: 1_000_000, text: "🎤 Voice message", file: clip)
        var failedClip = clip
        failedClip.id = "b0d8"
        failedClip.status = "failed"
        let twin = ChatMessage(key: "h:b0d8", ts: 1_009_000, text: "📎 voice (12.2 KB)", file: failedClip, wireId: "b0d8")
        let alone = ChatMessage(key: "h:77aa", ts: 2_000_000, file: ChatFile(id: "77aa", name: "sent-from-the-cli.pdf", size: 5, status: "failed"), wireId: "77aa")
        var theirs = twin
        theirs.key = "h:9f"
        theirs.from = "02ab"
        XCTAssertEqual(Skychat.withoutFailedTwins([bubble, twin, alone, theirs]), [bubble, alone, theirs])
        XCTAssertEqual(Skychat.withoutFailedTwins([bubble, alone]), [bubble, alone])
    }
}
