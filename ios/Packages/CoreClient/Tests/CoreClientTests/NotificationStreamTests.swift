@testable import CoreClient
import XCTest

/// The notification hub's stream (M3): the event-stream reader on its own,
/// then `CoreClient.notifications()` against FakeVisor's session rules with
/// the stream queued the way the visor writes it
/// (pkg/visor/hypervisor_handlers_notify.go: an opening comment, `data:`
/// events, `: ping` comments).
final class NotificationStreamTests: XCTestCase {
    private var server: StubServer?

    override func tearDown() async throws {
        server?.stop()
    }

    // MARK: The reader

    func testReaderJoinsChunksSplitAnywhere() {
        var reader = ServerSentEvents()
        let wire = ": connected\n\ndata: {\"a\":1}\n\n: ping\n\ndata: two\r\n\r\n"
        var events: [String] = []
        // One byte at a time: every split a network can make.
        for byte in Data(wire.utf8) {
            events += reader.feed(Data([byte]))
        }
        XCTAssertEqual(events, [#"{"a":1}"#, "two"])
    }

    func testReaderJoinsDataLinesAndKeepsAnUnfinishedEvent() {
        var reader = ServerSentEvents()
        XCTAssertEqual(reader.feed(Data("data: one\ndata:two\nid: 7\n\ndata: unfinished\n".utf8)), ["one\ntwo"])
        XCTAssertEqual(reader.feed(Data("\n".utf8)), ["unfinished"])
        XCTAssertEqual(reader.feed(Data(": only a comment\n\n\n".utf8)), [])
    }

    // MARK: The stream

    private static let password = "Str3am!pass"

    private func client(_ visor: FakeVisor) async throws -> CoreClient {
        let server = try await visor.start()
        self.server = server
        return CoreClient(transport: LoopbackTransport(origin: server.origin), password: { Self.password })
    }

    private static func chunk(_ text: String) -> Data { Data(text.utf8) }

    /// No session yet: the first open is refused (401), the client logs in
    /// and opens it again, and the events arrive in order, whole, whatever
    /// the chunking. Pings and the opening comment are not events; an event
    /// with neither title nor body is dropped; a missing tag is empty. The
    /// sequence ends when the visor ends the stream.
    func testStreamLogsInAndDeliversEvents() async throws {
        let visor = FakeVisor(accountPassword: Self.password)
        visor.queue("GET", CoreClient.notificationsPath, .stream(
            status: 200,
            headers: ["Content-Type": "text/event-stream"],
            chunks: [
                Self.chunk(": connected\n\n"),
                Self.chunk(#"data: {"app":"skychat","title":"Alice","body":"hi","tag":"02ab"}"# + "\n\n"),
                Self.chunk(": ping\n\n"),
                Self.chunk(#"data: {"app":"visor","title":"","body":""}"# + "\n\n"),
                Self.chunk(#"data: {"app":"skydex-client","ti"#),
                Self.chunk(#"tle":"Order filled","body":"1 SKY"}"# + "\n\n"),
            ],
            interval: .milliseconds(30),
            close: true
        ))
        let client = try await client(visor)

        var events: [NotifyEvent] = []
        for try await event in try await client.notifications() {
            events.append(event)
        }
        XCTAssertEqual(events, [
            NotifyEvent(app: "skychat", title: "Alice", body: "hi", tag: "02ab"),
            NotifyEvent(app: "skydex-client", title: "Order filled", body: "1 SKY"),
        ])
        let streamOpens = server?.requests.filter { $0.path == CoreClient.notificationsPath } ?? []
        XCTAssertEqual(streamOpens.count, 2, "one refused open, one after the login")
        XCTAssertNil(streamOpens.first?.header("Cookie"))
        XCTAssertNotNil(streamOpens.last?.header("Cookie"))
        XCTAssertEqual(visor.served.suffix(2), ["user-exists-true", "login"])
    }

    /// Anything but a stream is an error the caller sees (and retries).
    func testARefusedStreamThrows() async throws {
        let visor = FakeVisor(accountPassword: Self.password)
        visor.queue("GET", CoreClient.notificationsPath, .response(
            status: 503,
            headers: ["Content-Type": "text/plain"],
            body: Data("notification hub unavailable\n".utf8)
        ))
        let client = try await client(visor)
        do {
            _ = try await client.notifications()
            XCTFail("a 503 opened a stream")
        } catch let CoreClientError.http(_, path, status, _) {
            XCTAssertEqual(path, CoreClient.notificationsPath)
            XCTAssertEqual(status, 503)
        }
    }

    /// A stream that opened and then fell silent past the idle limit (a core
    /// stopped under it without closing it; the hub pings every 20 s) ends
    /// with an error, which is what tells the caller to open another.
    func testASilentStreamIsDropped() async throws {
        let visor = FakeVisor(accountPassword: Self.password)
        visor.queue("GET", CoreClient.notificationsPath, .stream(
            status: 200,
            headers: ["Content-Type": "text/event-stream"],
            chunks: [Self.chunk(": connected\n\n")],
            interval: .milliseconds(10),
            close: false
        ))
        let client = try await client(visor)
        let events = try await client.notifications(idleLimit: 1)
        let started = ContinuousClock.now
        do {
            for try await _ in events {}
            XCTFail("a silent stream finished cleanly")
        } catch {
            XCTAssertLessThan(ContinuousClock.now - started, .seconds(10), "dropped only after \(ContinuousClock.now - started)")
        }
    }
}
