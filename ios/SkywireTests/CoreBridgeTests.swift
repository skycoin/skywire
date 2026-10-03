import CoreBridge
import Darwin
import XCTest

/// The Go core inside an iOS process, through CoreBridge: start, answer on
/// the local API, stop, and do it again. The bundle has no host app, so the
/// core runs in the test process and the app is not launched: there is one
/// core per process.
///
/// Each start is a real visor on a phone-style config (it dials the dmsg
/// network), so a cycle takes seconds, not milliseconds. The API binds the
/// phone's address, 127.0.0.1:8000 — on the Simulator that is the Mac's
/// loopback, so nothing else (the app, a desktop visor) may hold it.
final class CoreBridgeTests: XCTestCase {
    private let core = CoreBridge.shared
    private static let apiPort: UInt16 = 8000
    private var dataDir: URL!

    override func setUp() async throws {
        try await super.setUp()
        XCTAssertEqual(
            tcpConnectErrno(port: Self.apiPort), ECONNREFUSED,
            "127.0.0.1:8000 is already taken (the app or a desktop visor?); stop it and rerun"
        )
        dataDir = FileManager.default.temporaryDirectory
            .appendingPathComponent("corebridge-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        try await core.configGen(
            outPath: configPath,
            options: GenOptions(binPath: dataDir.appendingPathComponent("bin").path)
        )
        try confineConfig()
    }

    override func tearDown() async throws {
        try? await core.stop(timeout: 30)
        if let dataDir {
            try? FileManager.default.removeItem(at: dataDir)
        }
        try await super.tearDown()
    }

    /// start → /api/ping answers "PONG!" → stop → the port refuses, three
    /// times in one process (the in-process restart the Go side's
    /// TestStartStopStart covers, here across the C boundary).
    func testStartPingStopThreeCycles() async throws {
        for cycle in 1...3 {
            try await core.start(configPath: configPath, dataDir: dataDir.path)
            XCTAssertEqual(core.state, .running, "cycle \(cycle): state after start")
            let pong = try await ping()
            XCTAssertEqual(pong, "\"PONG!\"", "cycle \(cycle): /api/ping")

            try await core.stop(timeout: 30)
            XCTAssertEqual(core.state, .stopped, "cycle \(cycle): state after stop")
            XCTAssertEqual(
                tcpConnectErrno(port: Self.apiPort), ECONNREFUSED,
                "cycle \(cycle): 127.0.0.1:8000 still accepts after stop"
            )
        }
    }

    /// A stop that arrives while the start is still bringing the visor up
    /// aborts it within the stop's 10 s budget and leaves no listener; a
    /// normal start works afterwards.
    func testStopDuringStart() async throws {
        let config = configPath
        let dir = dataDir.path
        let start = Task { try await CoreBridge.shared.start(configPath: config, dataDir: dir) }
        try await Task.sleep(nanoseconds: 200_000_000)

        let clock = ContinuousClock()
        let began = clock.now
        try await core.stop(timeout: 10)
        let took = clock.now - began
        XCTAssertLessThan(took, .seconds(10), "stop during start took \(took)")

        // The start either lost the race (stopped while starting) or won it
        // and was then stopped; both leave the core stopped.
        _ = try? await start.value
        XCTAssertEqual(core.state, .stopped)
        XCTAssertEqual(tcpConnectErrno(port: Self.apiPort), ECONNREFUSED, "a listener survived the aborted start")

        try await core.start(configPath: configPath, dataDir: dataDir.path)
        let pong = try await ping()
        XCTAssertEqual(pong, "\"PONG!\"")
    }

    /// A failing call throws with the core's reason, and skywire_last_error
    /// (copied and freed by CoreBridge) says the same.
    func testStartFailureCarriesTheReason() async throws {
        let missing = dataDir.appendingPathComponent("no-such-config.json").path
        do {
            try await core.start(configPath: missing, dataDir: dataDir.path)
            XCTFail("start with a missing config succeeded")
        } catch let error as CoreError {
            XCTAssertTrue(error.message.contains("config"), "reason: \(error.message)")
            XCTAssertEqual(core.lastError, error.message)
        }
        XCTAssertEqual(core.state, .failed)
    }

    // MARK: - Fixture

    private var configPath: String {
        dataDir.appendingPathComponent("skywire-config.json").path
    }

    /// Keeps the generated config inside the test's directory and off the
    /// Mac's other ports: no RPC, pty or local-relay listener, no STCP or LAN
    /// dmsg listener, absolute paths. (The app's ConfigProfile does the same
    /// and more; this is only what a test core needs.)
    private func confineConfig() throws {
        let url = URL(fileURLWithPath: configPath)
        var config = try XCTUnwrap(
            JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any]
        )
        let local = dataDir.appendingPathComponent("local").path
        config["cli_addr"] = ""
        config["pty"] = nil
        config["skywire-tcp"] = nil
        config["dmsgscp"] = ["disabled": true]
        config["local_path"] = local
        if var dmsg = config["dmsg"] as? [String: Any] {
            dmsg["local_relay"] = ["enabled": false]
            config["dmsg"] = dmsg
        }
        if var transport = config["transport"] as? [String: Any] {
            transport["log_store"] = ["location": local + "/transport_logs"]
            config["transport"] = transport
        }
        if var hypervisor = config["hypervisor"] as? [String: Any] {
            hypervisor["lan_dmsg_server"] = nil
            hypervisor["db_path"] = dataDir.appendingPathComponent("users.db").path
            config["hypervisor"] = hypervisor
        }
        try JSONSerialization.data(withJSONObject: config, options: .prettyPrinted).write(to: url)
    }

    /// GET /api/ping on a fresh connection.
    private func ping() async throws -> String {
        var request = URLRequest(url: URL(string: "http://127.0.0.1:\(Self.apiPort)/api/ping")!, timeoutInterval: 5)
        request.setValue("close", forHTTPHeaderField: "Connection")
        let (data, response) = try await URLSession(configuration: .ephemeral).data(for: request)
        XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 200)
        return String(decoding: data, as: UTF8.self)
    }

    /// Connects to 127.0.0.1:port and returns 0 on success or the connect
    /// errno: ECONNREFUSED is "nothing listens", which a URL error cannot
    /// tell apart from a listener that closed the connection.
    private func tcpConnectErrno(port: UInt16) -> Int32 {
        let fd = socket(AF_INET, SOCK_STREAM, 0)
        guard fd >= 0 else { return errno }
        defer { close(fd) }
        var address = sockaddr_in()
        address.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        address.sin_family = sa_family_t(AF_INET)
        address.sin_port = port.bigEndian
        address.sin_addr.s_addr = inet_addr("127.0.0.1")
        let result = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                connect(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        return result == 0 ? 0 : errno
    }
}
