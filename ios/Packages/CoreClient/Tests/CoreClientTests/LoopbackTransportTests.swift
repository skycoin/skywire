@testable import CoreClient
import XCTest

/// The loopback transport on its own: plain exchanges, streamed bodies, and
/// how each kind of request ends.
final class LoopbackTransportTests: XCTestCase {
    private var server: StubServer?

    override func tearDown() async throws {
        server?.stop()
    }

    private func serve(_ handler: @escaping StubServer.Handler) async throws -> LoopbackTransport {
        let server = try await StubServer.start(handler: handler)
        self.server = server
        return LoopbackTransport(origin: server.origin)
    }

    func testSendCarriesMethodHeadersAndBody() async throws {
        let transport = try await serve { request in
            .response(
                status: 201,
                headers: ["Content-Type": "application/json", "X-Echo": request.header("X-Token") ?? ""],
                body: request.body
            )
        }
        let response = try await transport.send(HTTPRequest(
            method: .put, path: "/api/thing?x=1", headers: ["X-Token": "t"], body: Data("{\"a\":1}".utf8)
        ))
        XCTAssertEqual(response.status, 201)
        XCTAssertEqual(response.header("x-echo"), "t")
        XCTAssertEqual(response.header("Content-Type"), "application/json")
        XCTAssertEqual(response.body, Data("{\"a\":1}".utf8))
        let request = try XCTUnwrap(server?.requests.first)
        XCTAssertEqual(request.method, "PUT")
        XCTAssertEqual(request.target, "/api/thing?x=1")
    }

    /// The transport keeps no cookies of its own: CoreClient does.
    func testSendIgnoresCookies() async throws {
        let transport = try await serve { _ in
            .response(status: 200, headers: ["Set-Cookie": "swm-session=abc; Path=/"], body: Data())
        }
        let first = try await transport.send(HTTPRequest(path: "/a"))
        XCTAssertEqual(first.header("Set-Cookie"), "swm-session=abc; Path=/")
        _ = try await transport.send(HTTPRequest(path: "/b"))
        XCTAssertNil(server?.requests.last?.header("Cookie"))
    }

    /// The chunks arrive as the server writes them, not at the end. (The head
    /// itself comes with the first chunk: the system's HTTP stack, through a
    /// task delegate and `bytes(for:)` alike, surfaces a response only once
    /// body bytes arrive; measured at 0.85 s for a first chunk sent at 0.8 s.)
    func testStreamDeliversTheBodyAsItArrives() async throws {
        let events = ["data: one\n\n", "data: two\n\n", "data: three\n\n"].map { Data($0.utf8) }
        let transport = try await serve { _ in
            .stream(status: 200, headers: ["Content-Type": "text/event-stream"], chunks: events, interval: .milliseconds(150), close: true)
        }
        let started = ContinuousClock.now
        let stream = try await transport.stream(HTTPRequest(path: "/api/notifications/stream", timeout: nil))
        XCTAssertEqual(stream.status, 200)
        XCTAssertEqual(stream.header("content-type"), "text/event-stream")
        var received = Data()
        var arrivals: [Duration] = []
        for try await chunk in stream.body {
            received.append(chunk)
            arrivals.append(ContinuousClock.now - started)
        }
        XCTAssertEqual(received, events.reduce(Data(), +))
        let first = try XCTUnwrap(arrivals.first)
        let last = try XCTUnwrap(arrivals.last)
        XCTAssertGreaterThan(last - first, .milliseconds(200), "the body was held back and delivered at the end")
    }

    /// A stream the server keeps open ends when its reader stops: the
    /// connection closes, and the server sees it.
    func testCancellingTheReaderClosesTheConnection() async throws {
        let transport = try await serve { _ in
            .stream(status: 200, headers: [:], chunks: [Data("data: hello\n\n".utf8)], interval: .zero, close: false)
        }
        let reader = Task {
            let stream = try await transport.stream(HTTPRequest(path: "/stream", timeout: nil))
            var chunks = 0
            for try await _ in stream.body {
                chunks += 1
            }
            return chunks
        }
        let server = try XCTUnwrap(self.server)
        try await waitUntil { server.requests.count == 1 }
        try await Task.sleep(for: .milliseconds(200))
        reader.cancel()
        _ = try? await reader.value
        try await waitUntil { server.clientClosedStreams == 1 }
    }

    func testSendGivesUpAfterItsTimeout() async throws {
        let transport = try await serve { _ in .silence }
        let started = ContinuousClock.now
        do {
            _ = try await transport.send(HTTPRequest(path: "/slow", timeout: 0.5))
            XCTFail("a silent server answered")
        } catch let error as URLError {
            XCTAssertEqual(error.code, .timedOut)
        }
        XCTAssertLessThan(ContinuousClock.now - started, .seconds(5))
    }

    func testRefusedConnectionThrows() async throws {
        // Port 9 (discard) is closed on a development Mac and on CI runners.
        let transport = LoopbackTransport(origin: URL(string: "http://127.0.0.1:9")!)
        do {
            _ = try await transport.send(HTTPRequest(path: "/api/ping", timeout: 2))
            XCTFail("a closed port answered")
        } catch let error as URLError {
            XCTAssertEqual(error.code, .cannotConnectToHost)
        }
    }

    private func waitUntil(timeout: Duration = .seconds(3), _ condition: @escaping @Sendable () -> Bool) async throws {
        let deadline = ContinuousClock.now + timeout
        while !condition() {
            guard ContinuousClock.now < deadline else {
                XCTFail("condition not met within \(timeout)")
                return
            }
            try await Task.sleep(for: .milliseconds(20))
        }
    }
}
