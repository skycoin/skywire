import CoreBridge
import CoreClient
import Foundation
import NetworkExtension

/// The core in the packet-tunnel extension (proposal §5.2): the host on a
/// device, where the extension is the only process iOS keeps running in the
/// background. A start prepares the config in the App Group container, as
/// `InAppCoreHost` does in the app's own, then installs the app's tunnel and
/// starts it; the extension (ios/PacketTunnel) starts the core on that config
/// and connects once the visor's local API is up.
actor TunnelCoreHost: CoreHost {
    /// The visor can take this long to come up (TPD retries on a cold start).
    static let startTimeout: TimeInterval = 180
    static let stopTimeout: TimeInterval = 45

    private let paths: CorePaths
    private let secrets: SecretStore
    private let settings: @MainActor @Sendable () -> ProfileSettings
    private let providerBundleID: String

    init(paths: CorePaths, secrets: SecretStore, providerBundleID: String, settings: @escaping @MainActor @Sendable () -> ProfileSettings) {
        self.paths = paths
        self.secrets = secrets
        self.providerBundleID = providerBundleID
        self.settings = settings
    }

    func start() async throws {
        try await ConfigProfile.prepare(paths: paths, settings: await settings(), secrets: secrets)
        let manager = try await Self.installedManager(providerBundleID)
        try manager.connection.startVPNTunnel()
        try await Self.wait(for: manager.connection, toReach: .connected, timeout: Self.startTimeout)
    }

    func stop() async throws {
        guard let manager = try await Self.existingManager(providerBundleID) else { return }
        manager.connection.stopVPNTunnel()
        try await Self.wait(for: manager.connection, toReach: .disconnected, timeout: Self.stopTimeout)
    }

    func restart() async throws {
        try await stop()
        try await start()
    }

    /// Deletes the local API's account store; call with the core stopped.
    func resetAccount() throws {
        guard FileManager.default.fileExists(atPath: paths.usersDB.path) else { return }
        try FileManager.default.removeItem(at: paths.usersDB)
    }

    /// The tunnel's status as the core's state, each change as iOS reports it.
    nonisolated var state: AsyncStream<CoreState> {
        let bundleID = providerBundleID
        return AsyncStream { continuation in
            let watcher = Task {
                var last: CoreState?
                while !Task.isCancelled {
                    let manager = try? await Self.existingManager(bundleID)
                    let now = manager.map { Self.coreState($0.connection.status) } ?? .stopped
                    if now != last {
                        continuation.yield(now)
                        last = now
                    }
                    // A change wakes this at once; the timer catches a manager that appears later.
                    let changes = NotificationCenter.default.notifications(named: .NEVPNStatusDidChange)
                    _ = await Self.first(of: changes, within: .seconds(2))
                }
                continuation.finish()
            }
            continuation.onTermination = { _ in watcher.cancel() }
        }
    }

    // MARK: The tunnel's configuration

    /// This app's tunnel as saved in the system's VPN settings, or nil when it
    /// was never installed.
    static func existingManager(_ bundleID: String) async throws -> NETunnelProviderManager? {
        try await NETunnelProviderManager.loadAllFromPreferences().first {
            ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == bundleID
        }
    }

    /// This app's tunnel, installed and enabled. The first save asks the user
    /// to allow a VPN configuration. A manager has to be loaded again after a
    /// save before it can start (Apple's NETunnelProviderManager guidance).
    static func installedManager(_ bundleID: String) async throws -> NETunnelProviderManager {
        let manager = try await existingManager(bundleID) ?? NETunnelProviderManager()
        let configuration = NETunnelProviderProtocol()
        configuration.providerBundleIdentifier = bundleID
        configuration.serverAddress = "Skywire"
        manager.protocolConfiguration = configuration
        manager.localizedDescription = "Skywire"
        manager.isEnabled = true
        try await manager.saveToPreferences()
        try await manager.loadFromPreferences()
        return manager
    }

    static func coreState(_ status: NEVPNStatus) -> CoreState {
        switch status {
        case .connecting, .reasserting: .starting
        case .connected: .running
        case .disconnecting: .stopping
        case .invalid, .disconnected: .stopped
        @unknown default: .stopped
        }
    }

    /// Waits until `connection` reaches `status`. A tunnel that drops back to
    /// disconnected while it should be connecting throws the reason iOS gives.
    private static func wait(for connection: NEVPNConnection, toReach status: NEVPNStatus, timeout: TimeInterval) async throws {
        let deadline = Date().addingTimeInterval(timeout)
        var started = false
        while Date() < deadline {
            let now = connection.status
            if now == status { return }
            if now == .connecting || now == .reasserting { started = true }
            if status == .connected, started, now == .disconnected || now == .invalid {
                // It throws the reason the tunnel last stopped, when iOS kept one.
                try await connection.fetchLastDisconnectError()
                throw TunnelHostError.stopped
            }
            let changes = NotificationCenter.default.notifications(named: .NEVPNStatusDidChange, object: connection)
            _ = await first(of: changes, within: .seconds(1))
        }
        throw TunnelHostError.timedOut(status)
    }

    /// Waits for the next element of `sequence`, at most `limit`.
    private static func first(of sequence: NotificationCenter.Notifications, within limit: Duration) async -> Bool {
        await withTaskGroup(of: Bool.self) { group in
            group.addTask {
                for await _ in sequence { return true }
                return false
            }
            group.addTask {
                try? await Task.sleep(for: limit)
                return false
            }
            let result = await group.next() ?? false
            group.cancelAll()
            return result
        }
    }
}

enum TunnelHostError: LocalizedError {
    case stopped
    case timedOut(NEVPNStatus)

    var errorDescription: String? {
        switch self {
        case .stopped: "the packet tunnel stopped while it was starting"
        case let .timedOut(status): "the packet tunnel did not reach status \(status.rawValue) in time"
        }
    }
}

/// Where the core runs (proposal §5.2): in the packet-tunnel extension on a
/// device, in the app on the Simulator, which runs no extension (G7's probe).
/// The launch argument `-SkywireCoreHost app|tunnel`, or Settings ▸ Logs &
/// diagnostics in a debug build, overrides that until the next launch that
/// reads it: a device's debugging, and the Simulator's probe.
enum CoreHostChoice: String, CaseIterable {
    case app, tunnel

    static let defaultsKey = "SkywireCoreHost"

    static func current(_ defaults: UserDefaults = .standard) -> CoreHostChoice {
        if let raw = defaults.string(forKey: defaultsKey), let choice = CoreHostChoice(rawValue: raw) {
            return choice
        }
        #if targetEnvironment(simulator)
        return .app
        #else
        return .tunnel
        #endif
    }
}
