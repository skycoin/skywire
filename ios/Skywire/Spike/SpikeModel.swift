import CoreBridge
import Foundation
import os

/// The M1 spike screen's state: the core's lifecycle, a ping of its local
/// API, the process footprint and the log tail.
@MainActor
final class SpikeModel: ObservableObject {
    @Published private(set) var state: CoreState = .stopped
    @Published private(set) var busy = false
    @Published private(set) var lastError: String?
    @Published private(set) var ping: Ping?
    @Published private(set) var footprint: UInt64?
    @Published private(set) var logLines: [String] = []

    struct Ping {
        let text: String
        let ok: Bool
        let at: Date
    }

    /// Whether the user left the core connected. Kept so a relaunch (after a
    /// force-quit, or iOS ending the suspended app) connects again, as
    /// Android's START_STICKY core service comes back after its process dies.
    private static let wantsConnectedKey = "core.wantsConnected"

    private let host: any CoreHost
    private let log: CoreLog
    private let footprintLog = Logger(subsystem: Bundle.main.bundleIdentifier ?? "skywire", category: "footprint")
    private var logGeneration: UInt64 = 0
    private var started = false

    init(host: any CoreHost, log: CoreLog) {
        self.host = host
        self.log = log
    }

    /// The app's model: the core in this process, under Application Support.
    static func inApp() -> SpikeModel {
        let log = CoreLog()
        // Before any start, so the core's first lines reach the tail.
        CoreBridge.shared.setLogSink(log.sink)
        return SpikeModel(host: InAppCoreHost(paths: .appSupport()), log: log)
    }

    /// Starts the model's loops once, and connects if the user left the core
    /// connected.
    func launch() {
        guard !started else { return }
        started = true
        Task { [weak self, host] in
            for await state in host.state {
                guard let self else { return }
                self.state = state
                if state == .running { await self.refreshPing() }
            }
        }
        Task { [weak self] in
            var tick = 0
            while let self {
                self.refreshFootprint(logIt: tick % 12 == 0)
                tick += 1
                try? await Task.sleep(nanoseconds: 5_000_000_000)
            }
        }
        Task { [weak self] in
            while let self {
                self.refreshLog()
                try? await Task.sleep(nanoseconds: 1_000_000_000)
            }
        }
        if UserDefaults.standard.bool(forKey: Self.wantsConnectedKey) {
            connect()
        }
    }

    func connect() {
        UserDefaults.standard.set(true, forKey: Self.wantsConnectedKey)
        perform { [host] in try await host.start() }
    }

    func disconnect() {
        UserDefaults.standard.set(false, forKey: Self.wantsConnectedKey)
        perform { [host] in try await host.stop() }
    }

    func restart() {
        perform { [host] in try await host.restart() }
    }

    /// Back in the foreground: the numbers on screen are minutes old.
    func becameActive() {
        refreshFootprint(logIt: true)
        refreshLog()
        if state == .running {
            Task { await refreshPing() }
        }
    }

    private func perform(_ operation: @escaping @Sendable () async throws -> Void) {
        busy = true
        lastError = nil
        Task {
            do {
                try await operation()
            } catch {
                // A start that Disconnect aborted fails with "stopped while
                // starting"; that is the user's doing, not an error to show.
                if UserDefaults.standard.bool(forKey: Self.wantsConnectedKey) || CoreBridge.shared.state != .stopped {
                    lastError = error.localizedDescription
                }
            }
            busy = false
            await refreshPing()
        }
    }

    /// GET /api/ping, which answers without a login. A fresh connection each
    /// time: a kept-alive one would outlive a core restart and fail once.
    func refreshPing() async {
        var request = URLRequest(url: Self.pingURL, timeoutInterval: 3)
        request.setValue("close", forHTTPHeaderField: "Connection")
        do {
            let (data, response) = try await Self.session.data(for: request)
            let status = (response as? HTTPURLResponse)?.statusCode ?? 0
            let body = String(decoding: data, as: UTF8.self)
            ping = Ping(text: status == 200 ? body : "HTTP \(status) \(body)", ok: status == 200, at: Date())
        } catch {
            ping = Ping(text: error.localizedDescription, ok: false, at: Date())
        }
    }

    private func refreshFootprint(logIt: Bool) {
        footprint = Footprint.current()
        // Once a minute into the unified log as well, so a measurement run
        // can be read back afterwards (`log show`) instead of off the screen.
        if logIt, let footprint {
            let mib = Double(footprint) / 1_048_576
            let core = state.description
            footprintLog.notice("phys_footprint \(mib, format: .fixed(precision: 1), privacy: .public) MiB, core \(core, privacy: .public)")
        }
    }

    private func refreshLog() {
        let tail = log.tail()
        guard tail.generation != logGeneration else { return }
        logGeneration = tail.generation
        logLines = tail.lines
    }

    private static let pingURL = URL(string: "http://\(ConfigProfile.apiAddress)/api/ping")!
    private static let session = URLSession(configuration: .ephemeral)
}
