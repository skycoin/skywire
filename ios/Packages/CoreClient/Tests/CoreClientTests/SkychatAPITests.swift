@testable import CoreClient
import XCTest

/// The native chat's skychat calls against a stub that answers like
/// cmd/apps/skychat/commands: Go's null for an empty slice, plain-text error
/// bodies, chunked SSE, multipart uploads.
final class SkychatAPITests: XCTestCase {
    private var server: StubServer?

    override func tearDown() async throws {
        server?.stop()
    }

    private func client(_ handler: @escaping StubServer.Handler) async throws -> SkychatClient {
        let server = try await StubServer.start(handler: handler)
        self.server = server
        return SkychatClient(transport: LoopbackTransport(origin: server.origin)) { "pw" }
    }

    private static func json(_ text: String, status: Int = 200) -> StubServer.Reply {
        .response(status: status, headers: ["Content-Type": "application/json"], body: Data(text.utf8))
    }

    func testNullListsAreEmpty() async throws {
        let client = try await client { _ in Self.json("null") }
        let history = try await client.history(peer: "02ab")
        let groups = try await client.groups()
        XCTAssertTrue(history.isEmpty)
        XCTAssertTrue(groups.isEmpty)
        XCTAssertEqual(server?.requests.first?.target, "/history?peer=02ab&limit=100")
        XCTAssertEqual(server?.requests.first?.header("Authorization"), SkychatClient.authorization(password: "pw"))
    }

    func testSendCarriesReceiptsAndReturnsTheId() async throws {
        let client = try await client { _ in Self.json(#"{"ok":true,"id":"m1"}"#) }
        let id = try await client.sendDm(pk: "02ab", wire: "hello", network: "dmsg")
        XCTAssertEqual(id, "m1")
        let body = try XCTUnwrap(server?.requests.first?.jsonObject)
        XCTAssertEqual(body["recipient"] as? String, "02ab")
        XCTAssertEqual(body["message"] as? String, "hello")
        XCTAssertEqual(body["network"] as? String, "dmsg")
        XCTAssertEqual(body["receipts"] as? Bool, true)
    }

    func testErrorBodiesComeBackAsTheMessage() async throws {
        let client = try await client { _ in .response(status: 400, headers: [:], body: Data("peer is blocked\n".utf8)) }
        do {
            _ = try await client.sendDm(pk: "02ab", wire: "x", network: "skynet")
            XCTFail("no error")
        } catch let error as SkychatError {
            XCTAssertEqual(error.status, 400)
            XCTAssertEqual(error.message, "peer is blocked")
        }
    }

    /// Some group actions answer `{info: …}`, others the bare record.
    func testGroupAnswersEitherShape() async throws {
        let client = try await client { request in
            request.path.hasSuffix("rotate-key")
                ? Self.json(#"{"info":{"id":"g1","key_epoch":7}}"#)
                : Self.json(#"{"id":"g1","name":"N","can_post":false,"members":null}"#)
        }
        let rotated = try await client.rotateKey(gid: "g1")
        let meta = try await client.refreshMeta(gid: "g1")
        XCTAssertEqual(rotated.keyEpoch, 7)
        XCTAssertEqual(meta.name, "N")
        XCTAssertFalse(meta.postable)
        XCTAssertTrue(meta.members.isEmpty)
    }

    func testUploadIsMultipartWithTheDestination() async throws {
        let client = try await client { _ in Self.json(#"{"id":"f1","file_url":"/files/a.txt"}"#) }
        let file = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + ".txt")
        try Data("file body".utf8).write(to: file)
        defer { try? FileManager.default.removeItem(at: file) }
        let sent = try await client.sendFile(destField: "group", destValue: "g1", file: file, name: "a \"b\".txt", mime: "text/plain") { _, _ in }
        XCTAssertEqual(sent.id, "f1")
        XCTAssertEqual(sent.fileUrl, "/files/a.txt")
        let request = try XCTUnwrap(server?.requests.first)
        XCTAssertTrue(request.header("Content-Type")?.hasPrefix("multipart/form-data; boundary=") == true)
        let body = String(decoding: request.body, as: UTF8.self)
        XCTAssertTrue(body.contains("name=\"group\"\r\n\r\ng1\r\n"))
        XCTAssertTrue(body.contains("filename=\"a %22b%22.txt\"\r\nContent-Type: text/plain\r\n\r\nfile body\r\n"))
    }

    func testEventsYieldEachMessage() async throws {
        let chunks = [
            Data(": hello\n\n".utf8),
            Data("data: {\"history\":\"true\"}\n\ndata: {\"channel\":\"dm-st".utf8),
            Data("atus\",\"peer\":\"02ab\",\"status\":\"read\",\"id\":\"m1\"}\n\n".utf8),
        ]
        let client = try await client { _ in
            .stream(status: 200, headers: ["Content-Type": "text/event-stream"], chunks: chunks, interval: .milliseconds(10), close: true)
        }
        var seen: [SseMessage] = []
        for try await message in try await client.events() { seen.append(message) }
        XCTAssertEqual(seen.count, 2)
        XCTAssertTrue(seen[0].history)
        XCTAssertEqual(seen[1].channel, "dm-status")
        XCTAssertEqual(seen[1].status, "read")
    }

    func testDownloadWritesTheBodyAndRefusesAnError() async throws {
        let client = try await client { request in
            request.path == "/files/ok.bin"
                ? .response(status: 200, headers: [:], body: Data([1, 2, 3]))
                : .response(status: 404, headers: [:], body: Data("not found".utf8))
        }
        let target = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: target) }
        try await client.download("/files/ok.bin", to: target)
        XCTAssertEqual(try Data(contentsOf: target), Data([1, 2, 3]))
        let missing = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        do {
            try await client.download("/files/gone.bin", to: missing)
            XCTFail("no error")
        } catch let error as SkychatError {
            XCTAssertEqual(error.status, 404)
        }
        XCTAssertFalse(FileManager.default.fileExists(atPath: missing.path))
    }

    func testPresenceKeysAreLowerCase() async throws {
        let client = try await client { _ in Self.json(#"{"peers":{"02AB":{"online":true,"rtt_ms":40}}}"#) }
        let peers = try await client.presence(["02ab"])
        XCTAssertEqual(peers["02ab"]?.rttMs, 40)
        XCTAssertEqual(server?.requests.first?.jsonObject?["pks"] as? [String], ["02ab"])
    }
}
