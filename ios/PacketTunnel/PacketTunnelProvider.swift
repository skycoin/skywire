import CoreBridge
import CoreClient
import NetworkExtension
import os

/// The packet-tunnel extension: on a device, the process the core runs in
/// (proposal §5.2), because iOS keeps it alive while the app is suspended.
/// The app prepares the config in the App Group container and starts the
/// tunnel (TunnelCoreHost); this starts the core on that config, routes
/// nothing until vpn-client asks for a tunnel, and answers the app's
/// provider messages on the core's loopback API.
///
/// `@unchecked Sendable`: the system calls a provider's methods one after
/// another, and `follower` is touched only from start and stop.
final class PacketTunnelProvider: NEPacketTunnelProvider, @unchecked Sendable {
    private static let log = Logger(subsystem: Bundle.main.bundleIdentifier ?? "skywire.tunnel", category: "tunnel")
    private static let apiOrigin = URL(string: "http://127.0.0.1:8000")!
    private static let stopTimeout: TimeInterval = 30

    private let messages = ProviderMessageHandler(transport: LoopbackTransport(origin: apiOrigin))
    private var follower: Task<Void, Never>?

    nonisolated(nonsending) override func startTunnel(options: [String: NSObject]? = nil) async throws {
        guard let group = Bundle.main.object(forInfoDictionaryKey: "SkywireAppGroup") as? String,
              let paths = CorePaths.appGroup(group)
        else { throw TunnelError(message: "the App Group container is missing") }
        CoreBridge.shared.setLogSink { level, line in
            Self.log.log(level: level.rawValue <= CoreLogLevel.error.rawValue ? .error : .info, "\(line, privacy: .public)")
        }
        try await setTunnelNetworkSettings(TunnelNetworkSettings.idle())
        if let fd = UtunDescriptor.find() {
            try CoreBridge.shared.setTunFD(fd)
        } else {
            Self.log.error("no utun descriptor found: SkyVPN cannot carry traffic")
        }
        try await CoreBridge.shared.start(configPath: paths.configFile.path, dataDir: paths.dataDir.path)
        follower = Task { [weak self] in await self?.followTunSettings() }
    }

    override func stopTunnel(with reason: NEProviderStopReason) async {
        follower?.cancel()
        follower = nil
        do {
            try await CoreBridge.shared.stop(timeout: Self.stopTimeout)
        } catch {
            Self.log.error("the core did not stop cleanly: \(error.localizedDescription, privacy: .public)")
        }
    }

    override func handleAppMessage(_ messageData: Data) async -> Data? {
        await messages.handle(messageData)
    }

    /// Applies each new tunnel vpn-client asks for: SkyVPN on routes everything
    /// through it, off routes nothing again. Polled, so nothing calls back
    /// into Swift from a Go thread.
    private func followTunSettings() async {
        var applied: UInt64 = 0
        while !Task.isCancelled {
            if let json = try? CoreBridge.shared.tunSettingsJSON(),
               let tun = try? JSONDecoder().decode(TunSettings.self, from: Data(json.utf8)),
               tun.seq != applied {
                do {
                    try await setTunnelNetworkSettings(TunnelNetworkSettings.vpn(tun) ?? TunnelNetworkSettings.idle())
                    applied = tun.seq
                    Self.log.notice("tunnel settings \(tun.seq) applied: \(tun.up ? tun.addr : "idle", privacy: .public)")
                } catch {
                    Self.log.error("tunnel settings \(tun.seq) refused: \(error.localizedDescription, privacy: .public)")
                }
            }
            try? await Task.sleep(for: .seconds(1))
        }
    }
}

struct TunnelError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}
