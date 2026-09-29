import CoreBridge
import Foundation

/// The files the core lives in.
struct CorePaths: Sendable {
    /// The core's working directory; everything it writes is under it.
    let dataDir: URL

    var configFile: URL { dataDir.appendingPathComponent("skywire-config.json") }
    var localDir: URL { dataDir.appendingPathComponent("local") }
    var binDir: URL { dataDir.appendingPathComponent("bin") }
    var usersDB: URL { dataDir.appendingPathComponent("users.db") }

    /// `Library/Application Support/skywire/` in the app's container. The
    /// directory is created by the first start.
    static func appSupport() -> CorePaths {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        return CorePaths(dataDir: base.appendingPathComponent("skywire", isDirectory: true))
    }
}

/// Runs the core inside the app process. This is the Simulator's host (the
/// packet-tunnel extension does not run there) and, later, a debug option on
/// a device. A suspended app's core is frozen with it, so this host is for
/// development, not for a phone in someone's pocket.
actor InAppCoreHost: CoreHost {
    /// How long a stop may take before it is reported as failed.
    static let stopTimeout: TimeInterval = 30

    private let paths: CorePaths
    private let core = CoreBridge.shared

    init(paths: CorePaths) {
        self.paths = paths
    }

    func start() async throws {
        try FileManager.default.createDirectory(at: paths.dataDir, withIntermediateDirectories: true)
        // Generated once, like Android's ConfigManager.ensureConfig: a new
        // config is a new identity. The profile is re-applied on every start
        // so its pins survive the visor rewriting the file at runtime.
        if !FileManager.default.fileExists(atPath: paths.configFile.path) {
            try await core.configGen(
                outPath: paths.configFile.path,
                options: GenOptions(hypervisorAddr: ConfigProfile.apiAddress, binPath: paths.binDir.path)
            )
        }
        try ConfigProfile.apply(to: paths)
        try await core.start(configPath: paths.configFile.path, dataDir: paths.dataDir.path)
    }

    func stop() async throws {
        try await core.stop(timeout: Self.stopTimeout)
    }

    func restart() async throws {
        try await stop()
        try await start()
    }

    /// Polls the core: its state also changes on its own (the visor's
    /// restart route stops and starts it from Go), so there is no single
    /// Swift call site to publish from.
    nonisolated var state: AsyncStream<CoreState> {
        AsyncStream { continuation in
            let poller = Task {
                var last: CoreState?
                while !Task.isCancelled {
                    let now = CoreBridge.shared.state
                    if now != last {
                        continuation.yield(now)
                        last = now
                    }
                    try? await Task.sleep(nanoseconds: 250_000_000)
                }
                continuation.finish()
            }
            continuation.onTermination = { _ in poller.cancel() }
        }
    }
}
