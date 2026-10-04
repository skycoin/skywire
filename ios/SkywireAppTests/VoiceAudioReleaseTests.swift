import AVFoundation
import CoreClient
@testable import Skywire
import Network
import os
import XCTest

/// `VoiceAudioEngine.stop` must hand the microphone back (Android:
/// VoiceAudioReleaseTest, an instrumented test there because both halves of
/// the claim are about real machinery: a real capture device held, and a
/// real TCP connection whose buffer really fills).
///
/// The two tests below are the two ways a call ends:
///
///  - the visor still draining the stream, where the writer never blocks
///    and only the engine's own flag gets it out;
///  - the visor having stopped reading — the ordinary case, since that is
///    what hanging up looks like — where the socket buffer fills, the writer
///    parks mid-write, and only cancelling the request can reach it. On iOS
///    there is no public "an input stream is open" to read the way Android
///    reads `activeRecordingConfigurations`, so the observables are the
///    engine's own: the writer count must reach zero (the upload's body
///    producer returned, so the request is gone) and the tap must stop
///    delivering buffers.
@MainActor
final class VoiceAudioReleaseTests: XCTestCase {

    private var visor: FakeVisor?
    private var engine: VoiceAudioEngine?

    override func setUp() async throws {
        // The microphone must be granted before the engine can hold it:
        // xcrun simctl privacy booted grant microphone com.skycoin.skywire
        guard AVAudioSession.sharedInstance().recordPermission == .granted else {
            XCTFail("the microphone is not granted — run: xcrun simctl privacy booted grant microphone com.skycoin.skywire")
            return
        }
    }

    override func tearDown() async throws {
        engine?.stop()
        engine = nil
        visor?.close()
        visor = nil
    }

    /// The visor is reading the stream — nothing blocks, and stop() must
    /// still land.
    func testStopReleasesTheMicrophone() async throws {
        try await assertStopReleasesTheMicrophone(drainBody: true)
    }

    /// The visor has stopped reading. The writer ends up parked mid-write
    /// with the microphone held, and only the request's cancellation can
    /// reach it.
    func testStopReleasesTheMicrophoneWhenTheVisorStopsReading() async throws {
        try await assertStopReleasesTheMicrophone(drainBody: false)
    }

    private func assertStopReleasesTheMicrophone(drainBody: Bool) async throws {
        let fake = FakeVisor(drainBody: drainBody)
        try fake.start()
        visor = fake
        let client = CoreClient(transport: LoopbackTransport(origin: fake.origin)) { "test" }
        let engine = VoiceAudioEngine()
        self.engine = engine

        engine.start(client: client)

        let opened = await waitFor(.seconds(15)) { engine.writersAndFrames.frames > 0 }
        XCTAssertTrue(opened, "the engine never opened the microphone — check the Simulator has an input device and the permission is granted")
        XCTAssertTrue(engine.capturing, "frames arrived but capturing reads false")
        // The tap starts before the upload, which first asks the visor for its
        // key and a CSRF token, so the writer can trail the first frames.
        let streaming = await waitFor(.seconds(15)) { engine.writersAndFrames.writers > 0 }
        XCTAssertTrue(streaming, "no upload writer is inside the stream")

        // Long enough that the writer is well inside the stream — and, when
        // the fake visor is not reading, long enough for the buffers to fill
        // at 96 KB/s and leave the writer genuinely blocked mid-write. That
        // blocked state is the whole point of the second case.
        try await Task.sleep(for: .seconds(3))

        engine.stop()

        let released = await waitFor(.seconds(5)) { engine.writersAndFrames.writers == 0 }
        XCTAssertTrue(released, "an upload writer was still inside the body 5 s after stop() — the request was never torn down, which is the microphone held for the life of the process")
        XCTAssertFalse(engine.capturing, "the tap is still installed after stop()")
        let frames = engine.writersAndFrames.frames
        try await Task.sleep(for: .milliseconds(700))
        XCTAssertEqual(engine.writersAndFrames.frames, frames, "the tap delivered audio after stop()")
    }

    @MainActor
    private func waitFor(_ budget: Duration, _ condition: @MainActor () -> Bool) async -> Bool {
        let deadline = ContinuousClock.now + budget
        while ContinuousClock.now < deadline {
            if condition() { return true }
            try? await Task.sleep(for: .milliseconds(50))
        }
        return condition()
    }
}

/// The routes CoreClient touches on the way to opening a voice stream, on
/// the port the app's profile is hard-wired to (127.0.0.1:8000; the app
/// under test starts no core, so the port is free). Hand-rolled over
/// NWConnection: the behaviour that matters is a server that accepts an
/// upload and then STOPS reading it, which is a socket-level condition.
/// All state lives on the listener's queue.
final class FakeVisor: @unchecked Sendable {
    private let drainBody: Bool
    private var listener: NWListener?
    private let queue = DispatchQueue(label: "fake-visor")
    private var connections: [NWConnection] = []
    private var running = true

    static let pk = String(repeating: "0f", count: 33)

    init(drainBody: Bool) {
        self.drainBody = drainBody
    }

    var origin: URL { URL(string: "http://127.0.0.1:8000")! }

    func start() throws {
        let parameters = NWParameters.tcp
        parameters.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: 8000)
        let listener = try NWListener(using: parameters)
        let ready = DispatchSemaphore(value: 0)
        let failure = OSAllocatedUnfairLock<NWError?>(initialState: nil)
        listener.stateUpdateHandler = { state in
            switch state {
            case .ready:
                ready.signal()
            case let .failed(error), let .waiting(error):
                failure.withLock { $0 = error }
                ready.signal()
            default:
                break
            }
        }
        listener.newConnectionHandler = { [weak self] connection in
            self?.accept(connection)
        }
        listener.start(queue: queue)
        self.listener = listener
        _ = ready.wait(timeout: .now() + 5)
        if let error = failure.withLock({ $0 }) {
            listener.cancel()
            throw error
        }
    }

    /// Returns once the listener is gone: its cancel completes later on the
    /// queue, and a bind before then fails with EADDRINUSE (seen on CI).
    func close() {
        let cancelled = DispatchSemaphore(value: 0)
        let listener = queue.sync { () -> NWListener? in
            running = false
            let listener = self.listener
            self.listener = nil
            listener?.stateUpdateHandler = { if case .cancelled = $0 { cancelled.signal() } }
            listener?.cancel()
            connections.forEach { $0.cancel() }
            connections.removeAll()
            return listener
        }
        if listener != nil {
            _ = cancelled.wait(timeout: .now() + 5)
        }
    }

    private func accept(_ connection: NWConnection) {
        connections.append(connection)
        connection.start(queue: queue)
        readRequest(connection, buffer: Data())
    }

    private func readRequest(_ connection: NWConnection, buffer: Data) {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 1 << 16) { [weak self] data, _, isComplete, error in
            guard let self else { return }
            var buffer = buffer
            if let data { buffer.append(data) }
            if let request = Self.head(of: buffer) {
                self.route(request, remainder: buffer, on: connection)
            } else if isComplete || error != nil {
                connection.cancel()
            } else {
                self.readRequest(connection, buffer: buffer)
            }
        }
    }

    /// The request head (line and headers) once it has arrived, or nil.
    private static func head(of buffer: Data) -> (method: String, path: String)? {
        guard let end = buffer.range(of: Data("\r\n\r\n".utf8)) else { return nil }
        let lines = String(decoding: buffer[..<end.lowerBound], as: UTF8.self).components(separatedBy: "\r\n")
        let parts = lines[0].split(separator: " ")
        guard parts.count >= 2 else { return nil }
        return (String(parts[0]), String(parts[1]))
    }

    private func route(_ request: (method: String, path: String), remainder: Data, on connection: NWConnection) {
        switch request.path {
        case "/api/user":
            respond(connection, status: 401, body: #"{"error":"not logged in"}"#)
        case "/api/user-exists":
            respond(connection, status: 200, body: #"{"exists":false}"#)
        case "/api/create-account", "/api/login":
            var head = "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n"
            head += "Set-Cookie: swm-session=test\r\nContent-Length: 2\r\n\r\n{}"
            send(connection, Data(head.utf8))
        case "/api/csrf":
            respond(connection, status: 200, body: #"{"csrf_token":"test"}"#)
        case "/api/about":
            respond(connection, status: 200, body: #"{"public_key":"\#(Self.pk)"}"#)
        case let path where path.hasSuffix("/mic"):
            // The visor holds the mic POST open for the whole call: the
            // response only comes when the body ends. Either the body is
            // drained as fast as it arrives, or it is left alone until the
            // buffers fill and the client's next write blocks — the state
            // the second test exists for.
            if drainBody {
                drainBodyAfterHead(connection)
            }
        case let path where path.hasSuffix("/speaker"):
            var head = "HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\n"
            head += "Transfer-Encoding: chunked\r\n\r\n"
            send(connection, Data(head.utf8))
            // Headers, then silence: a call with nobody talking.
        default:
            respond(connection, status: 404, body: #"{"error":"not recorded"}"#)
        }
    }

    /// Keeps reading and discarding: a visor with a call still up.
    private func drainBodyAfterHead(_ connection: NWConnection) {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 1 << 16) { [weak self] data, _, isComplete, error in
            guard let self, self.running else { return }
            if let data, !isComplete, error == nil {
                self.drainBodyAfterHead(connection)
            } else {
                connection.cancel()
            }
        }
    }

    private func respond(_ connection: NWConnection, status: Int, body: String) {
        let bytes = Data(body.utf8)
        var head = "HTTP/1.1 \(status) OK\r\nContent-Type: application/json\r\n"
        head += "Content-Length: \(bytes.count)\r\n\r\n"
        send(connection, Data(head.utf8) + bytes)
    }

    private func send(_ connection: NWConnection, _ data: Data) {
        connection.send(content: data, completion: .contentProcessed { [weak self] _ in
            guard let self, self.running else { return }
            // Keep serving this connection: the session's requests are
            // pooled behind one another.
            self.readRequest(connection, buffer: Data())
        })
    }
}
