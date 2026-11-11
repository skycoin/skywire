@testable import CoreClient
import XCTest

/// The voice routes: the lists the call screen reads, the actions it sends,
/// and the two audio streams (a streamed upload for the microphone, a
/// streamed response for the speaker), against the FakeVisor's session rules.
final class VoiceEndpointTests: XCTestCase {
    private var server: StubServer?

    override func tearDown() async throws {
        server?.stop()
    }

    /// A logged-in client over the fake visor, with `routes` queued by
    /// request line (voice routes have no recordings).
    private func client(routes: [(String, String, StubServer.Reply)]) async throws -> CoreClient {
        let visor = FakeVisor()
        for (method, target, reply) in routes {
            visor.queue(method, target, reply)
        }
        let server = try await visor.start()
        self.server = server
        let client = CoreClient(transport: LoopbackTransport(origin: server.origin)) { "pw" }
        try await client.ensureSession()
        return client
    }

    private static let json = ["Content-Type": "application/json"]

    // --- the models ---

    func testInviteLinesParse() {
        let invite = VoiceInvite.parse("  abc-123  from  0327b61a11e662b6d1a5b04c79c7ea96e1c1e0d5e64bbaeba5014c1f65d0c433  ")
        XCTAssertEqual(invite, VoiceInvite(callId: "abc-123", fromPk: "0327b61a11e662b6d1a5b04c79c7ea96e1c1e0d5e64bbaeba5014c1f65d0c433"))
        XCTAssertNil(VoiceInvite.parse("no separator"))
        XCTAssertNil(VoiceInvite.parse(" from 03pk"))
        XCTAssertNil(VoiceInvite.parse("id from "))
    }

    /// The port of Android's DialProgressTest, parsing half.
    func testDialStatesParseAndSplitIntoProgressAndOutcome() {
        let progress = ["connecting": DialState.connecting, "calling": .calling, "ringing": .ringing]
        let outcomes = ["offline": DialState.offline, "declined": .declined, "busy": .busy, "no_answer": .noAnswer, "failed": .failed]
        for (wire, state) in progress {
            XCTAssertEqual(DialState.parse(wire), state)
            XCTAssertFalse(state.ended, "\(wire) is progress, not an outcome")
        }
        for (wire, state) in outcomes {
            XCTAssertEqual(DialState.parse(wire), state)
            XCTAssertTrue(state.ended, "\(wire) is an outcome")
        }
        // A visor that predates dial progress sends no state: the call is
        // just "calling".
        XCTAssertEqual(DialState.parse(""), .calling)
        XCTAssertEqual(DialState.parse("something-new"), .calling)
    }

    // --- the lists ---

    func testIncomingAndActiveLists() async throws {
        let pk = recordedPK
        let client = try await client(routes: [
            ("GET", "/api/visors/\(pk)/skychat/voice/incoming", .response(
                status: 200, headers: Self.json,
                body: Data("[\"call-1 from 03aaa\", \"call-2 from 03bbb\"]".utf8))),
            ("GET", "/api/visors/\(pk)/skychat/voice/active", .response(
                status: 200, headers: Self.json, body: Data(#"["call-2"]"#.utf8))),
            ("GET", "/api/visors/\(pk)/skychat/voice/dialing", .response(
                status: 200, headers: Self.json,
                body: Data(#"[{"call_id":"call-9","peer":"03ccc","state":"ringing","ringback":true}]"#.utf8))),
        ])
        let incoming = try await client.voiceIncoming()
        XCTAssertEqual(incoming.map(\.callId), ["call-1", "call-2"])
        XCTAssertEqual(incoming.map(\.fromPk), ["03aaa", "03bbb"])
        let active = try await client.voiceActive()
        XCTAssertEqual(active, ["call-2"])
        let dialing = try await client.voiceDialing()
        XCTAssertEqual(dialing, [OutgoingCall(callId: "call-9", peerPk: "03ccc", state: .ringing, ringback: true)])
    }

    /// An empty list is the body the visor sends between calls, a restart in
    /// progress answers 503, and a visor predating dial progress answers 404:
    /// all three are no calls, not a failure.
    func testEmptyAndAbsentCallLists() async throws {
        let pk = recordedPK
        let client = try await client(routes: [
            ("GET", "/api/visors/\(pk)/skychat/voice/incoming", .response(status: 200, headers: Self.json, body: Data("[]".utf8))),
            ("GET", "/api/visors/\(pk)/skychat/voice/active", .response(status: 200, headers: Self.json, body: Data("null".utf8))),
            ("GET", "/api/visors/\(pk)/skychat/voice/dialing", .response(status: 404, headers: Self.json, body: Data())),
        ])
        let incoming = try await client.voiceIncoming()
        XCTAssertEqual(incoming, [])
        let active = try await client.voiceActive()
        XCTAssertEqual(active, [])
        let dialing = try await client.voiceDialing()
        XCTAssertEqual(dialing, [])
    }

    // --- the actions ---

    func testCallPlacesAndActionsCarryTheirBodies() async throws {
        let pk = recordedPK
        let client = try await client(routes: [
            ("POST", "/api/visors/\(pk)/skychat/voice/call", .response(status: 200, headers: Self.json, body: Data(#"{"call_id":"c-1"}"#.utf8))),
            ("POST", "/api/visors/\(pk)/skychat/voice/answer", .response(status: 200, headers: Self.json, body: Data("{}".utf8))),
            ("POST", "/api/visors/\(pk)/skychat/voice/decline", .response(status: 200, headers: Self.json, body: Data("{}".utf8))),
            ("POST", "/api/visors/\(pk)/skychat/voice/hangup", .response(status: 200, headers: Self.json, body: Data("{}".utf8))),
            ("POST", "/api/visors/\(pk)/skychat/voice/mute", .response(status: 200, headers: Self.json, body: Data("{}".utf8))),
            ("GET", "/api/visors/\(pk)/skychat/voice/ringback?call=c-1", .response(status: 200, headers: ["Content-Type": "application/octet-stream"], body: Data([0x01, 0x02, 0x03]))),
        ])
        let callId = try await client.voiceCall(peer: "03peer")
        XCTAssertEqual(callId, "c-1")
        try await client.voiceAnswer(callId: callId)
        try await client.voiceDecline(callId: callId)
        try await client.voiceHangup(callId: callId)
        try await client.voiceMute(callId: callId, mic: true, speaker: false)
        let ringback = try await client.voiceRingback(callId: callId)
        XCTAssertEqual(ringback, Data([0x01, 0x02, 0x03]))

        // Only the voice actions; the session's own POSTs (create-account,
        // login) are in the log too.
        let bodies = (server?.requests ?? []).filter { $0.method == "POST" && $0.path.hasPrefix("/api/visors/") }.map { request in
            (request.path, request.jsonObject ?? [:])
        }
        XCTAssertEqual(bodies[0].1["peer"] as? String, "03peer")
        XCTAssertEqual(bodies[1].1["call_id"] as? String, "c-1")
        XCTAssertEqual(bodies[2].1["call_id"] as? String, "c-1")
        XCTAssertEqual(bodies[3].1["call_id"] as? String, "c-1")
        XCTAssertEqual(bodies[4].1["mic"] as? Bool, true)
        XCTAssertEqual(bodies[4].1["speaker"] as? Bool, false)
    }

    /// A failed call is an error, not an empty id: the caller's screen has
    /// to say it did not go through.
    func testAFailedCallPlacementThrows() async throws {
        let pk = recordedPK
        let client = try await client(routes: [
            ("POST", "/api/visors/\(pk)/skychat/voice/call", .response(status: 500, headers: Self.json, body: Data(#"{"error":"peer offline"}"#.utf8))),
        ])
        do {
            _ = try await client.voiceCall(peer: "03peer")
            XCTFail("a 500 answer must throw")
        } catch let error as CoreClientError {
            XCTAssertTrue("\(error)".contains("peer offline"))
        }
    }

    // --- the audio streams ---

    /// The microphone upload: the body the writer produces arrives intact,
    /// framed chunked (no length is known up front), and the response head —
    /// which the visor sends only when the call ends — comes back after it.
    /// A fresh client logs in on the way (the first upload carries no
    /// session cookie and is answered 401).
    func testMicStreamUploadsTheProducedBytes() async throws {
        let visor = FakeVisor()
        let payload = Data((0..<4096).map { UInt8($0 % 251) })
        visor.queue("POST", "/api/voice-audio/\(recordedPK)/mic", .response(
            status: 200, headers: ["Content-Type": "application/octet-stream"], body: Data("done".utf8)
        ))
        let server = try await visor.start()
        self.server = server
        let client = CoreClient(transport: LoopbackTransport(origin: server.origin)) { "pw" }

        let head = try await client.voiceMicStream { output in
            var sent = 0
            while sent < payload.count {
                let end = min(sent + 960, payload.count)
                let slice = payload.subdata(in: sent..<end)
                slice.withUnsafeBytes { raw in
                    _ = output.write(raw.baseAddress!.assumingMemoryBound(to: UInt8.self), maxLength: raw.count)
                }
                sent = end
            }
        }
        XCTAssertEqual(head.status, 200)

        let upload = try XCTUnwrap(server.requests.last { $0.path.hasSuffix("/mic") })
        XCTAssertEqual(upload.body, payload)
        XCTAssertEqual(upload.header("Content-Type"), "application/octet-stream")
        XCTAssertNotNil(upload.header("X-CSRF-Token"))
    }

    /// Cancelling the awaiting call is what ends an upload whose producer
    /// never returns: the request is torn down from outside.
    func testCancellingEndsAnUnfinishedUpload() async throws {
        let visor = FakeVisor()
        visor.queue("POST", "/api/voice-audio/\(recordedPK)/mic", .response(status: 200, headers: [:], body: Data()))
        let server = try await visor.start()
        self.server = server
        let client = CoreClient(transport: LoopbackTransport(origin: server.origin)) { "pw" }
        try await client.ensureSession()

        let upload = Task {
            try await client.voiceMicStream { output in
                var byte: UInt8 = 0x7f
                // Writes forever, like a tap that is never stopped.
                while true {
                    _ = output.write(&byte, maxLength: 1)
                }
            }
        }
        try await Task.sleep(for: .milliseconds(500))
        upload.cancel()
        do {
            _ = try await upload.value
            XCTFail("a cancelled upload must throw, not return")
        } catch is CancellationError {
        } catch {
            // The connection tearing down can also surface as a transport
            // error; either way the call returned.
        }
    }

    /// The speaker stream: the PCM chunks arrive in order, and a 401 on the
    /// way (a core restart dropped the session) costs one re-login, not the
    /// call.
    func testSpeakerStreamDeliversTheChunks() async throws {
        let first = Data([0x01, 0x02])
        let second = Data([0x03, 0x04])
        let visor = FakeVisor()
        // The fake answers 401 while no session is live, which the first
        // open is: the re-login below is the behaviour under test.
        visor.queue("GET", "/api/voice-audio/\(recordedPK)/speaker", .stream(
            status: 200, headers: ["Content-Type": "application/octet-stream"], chunks: [first, second], interval: .milliseconds(50), close: true
        ))
        let server = try await visor.start()
        self.server = server
        let client = CoreClient(transport: LoopbackTransport(origin: server.origin)) { "pw" }

        let stream = try await client.voiceSpeakerStream()
        XCTAssertEqual(stream.status, 200)
        var received = Data()
        for try await chunk in stream.body {
            received.append(chunk)
        }
        XCTAssertEqual(received, first + second)
    }
}
