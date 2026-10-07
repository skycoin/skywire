import CoreBridge
import CoreClient
import Foundation

/// Runs the core inside the app process. This is the Simulator's host (the
/// packet-tunnel extension does not run there) and, later, a debug option on
/// a device. A suspended app's core is frozen with it, so this host is for
/// development, not for a phone in someone's pocket.
actor InAppCoreHost: CoreHost {
    /// How long a stop may take before it is reported as failed.
    static let stopTimeout: TimeInterval = 30

    private let paths: CorePaths
    private let secrets: SecretStore
    private let settings: @MainActor @Sendable () -> ProfileSettings
    private let core = CoreBridge.shared

    /// - Parameter settings: the user's choices, read at every start.
    init(paths: CorePaths, secrets: SecretStore, settings: @escaping @MainActor @Sendable () -> ProfileSettings) {
        self.paths = paths
        self.secrets = secrets
        self.settings = settings
    }

    func start() async throws {
        try await ConfigProfile.prepare(paths: paths, settings: await settings(), secrets: secrets)
        try await core.start(configPath: paths.configFile.path, dataDir: paths.dataDir.path)
    }

    func stop() async throws {
        try await core.stop(timeout: Self.stopTimeout)
    }

    func restart() async throws {
        try await stop()
        try await start()
    }

    /// Deletes the local API's account store; call with the core stopped.
    /// The next start's login creates the account again with the Keychain's
    /// password (Android: ConfigManager.deleteUsersDb).
    func resetAccount() throws {
        guard FileManager.default.fileExists(atPath: paths.usersDB.path) else { return }
        try FileManager.default.removeItem(at: paths.usersDB)
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
