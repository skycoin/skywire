import CoreClient
import Foundation
import Network
import os

/// Tells the core when the network moves under it and whether it is metered (Android:
/// NetworkWatcher). Left alone the core notices a dead dmsg session only when a keepalive
/// fails, up to 75 s after a switch between Wi-Fi and cellular. Runs while the core's API
/// answers, so each start takes a new baseline.
@MainActor
final class NetworkWatcher {
    struct Timing {
        /// Long enough for a handover's updates to land.
        var settle = Duration.milliseconds(1500)
        var retry = Duration.seconds(2)
        /// How soon news the core missed is offered again.
        var pendingRetry = Duration.seconds(15)
    }

    private let timing: Timing
    private let send: (NetworkEvent) async throws -> NetworkReply
    private let log = Logger(subsystem: Bundle.main.bundleIdentifier ?? "skywire", category: "network")
    private var running = false
    private var monitor: NWPathMonitor?
    private var generation = 0
    private var moves = NetworkMoves()
    private var metered = MeteredFlag()
    private var meteredNow: Bool?
    private var ifname: String?
    private var movePending = false
    private var scheduled: Task<Void, Never>?
    private var scheduledIsRetry = false

    convenience init(client: CoreClient) {
        self.init(timing: Timing()) { try await client.networkEvent($0) }
    }

    init(timing: Timing, send: @escaping (NetworkEvent) async throws -> NetworkReply) {
        self.timing = timing
        self.send = send
    }

    /// Starts with a new baseline. `monitoring` false leaves the paths to `note`, for tests.
    func start(monitoring: Bool = true) {
        guard !running else { return }
        running = true
        generation += 1
        moves = NetworkMoves()
        metered = MeteredFlag()
        meteredNow = nil
        ifname = nil
        movePending = false
        guard monitoring else { return }
        let monitor = NWPathMonitor()
        monitor.pathUpdateHandler = { [weak self] path in
            let snapshot = NetworkPath(path)
            Task { @MainActor in self?.note(snapshot) }
        }
        monitor.start(queue: DispatchQueue(label: "com.skycoin.skywire.network-watcher"))
        self.monitor = monitor
    }

    func stop() {
        running = false
        monitor?.cancel()
        monitor = nil
        scheduled?.cancel()
        scheduled = nil
    }

    func note(_ path: NetworkPath) {
        guard running else { return }
        guard let interface = path.interface else {
            if moves.current != nil { log.notice("network lost") }
            moves.lost()
            return
        }
        ifname = interface
        meteredNow = path.metered
        var news = false
        if metered.observe(path.metered) {
            log.notice("network metered: \(path.metered, privacy: .public)")
            news = true
        }
        let was = moves.current
        let next = NetworkAttachment(interface: interface, addresses: path.addresses)
        if moves.observe(next) {
            log.notice("network moved: \(was?.interface ?? "none", privacy: .public) -> \(interface, privacy: .public) \(next.description, privacy: .private)")
            movePending = true
            news = true
        } else if was == nil {
            log.notice("network baseline: \(interface, privacy: .public) \(next.description, privacy: .private)")
        }
        if news { schedule(after: timing.settle, retry: false) }
    }

    /// One report for a burst of updates. Fresh news replaces a pending retry's long wait.
    private func schedule(after delay: Duration, retry: Bool) {
        if scheduled != nil {
            guard scheduledIsRetry, !retry else { return }
            scheduled?.cancel()
        }
        scheduledIsRetry = retry
        scheduled = Task { [weak self] in
            try? await Task.sleep(for: delay)
            guard !Task.isCancelled, let self else { return }
            self.scheduled = nil
            await self.report()
        }
    }

    private func report() async {
        let started = generation
        let moved = movePending
        movePending = false
        let event = NetworkEvent(event: moved ? "moved" : "changed", metered: meteredNow, ifname: ifname)
        for attempt in 1...2 {
            do {
                let reply = try await send(event)
                log.notice("core told \(event.event, privacy: .public) (metered \(event.metered ?? false, privacy: .public)): mode \(reply.mode, privacy: .public), \(reply.moves, privacy: .public) moves")
                return
            } catch {
                log.notice("network report failed: \(error.localizedDescription, privacy: .public)")
            }
            if attempt == 1 { try? await Task.sleep(for: timing.retry) }
        }
        // The core is still coming up or busy failing over: offer the same news again later.
        guard running, generation == started else { return }
        if moved { movePending = true }
        schedule(after: timing.pendingRetry, retry: true)
    }
}

/// What the watcher needs from a path, read on the monitor's queue.
struct NetworkPath: Sendable {
    /// The interface the core's sockets ride, nil while there is no network.
    let interface: String?
    let metered: Bool
    let addresses: Set<String>

    init(interface: String?, metered: Bool, addresses: Set<String>) {
        self.interface = interface
        self.metered = metered
        self.addresses = addresses
    }

    init(_ path: NWPath) {
        let usable = path.availableInterfaces.filter { $0.type != .loopback }
        // The network under a VPN, where the sockets are. The Simulator shows only the
        // host's route, a host VPN's utun included, and calls it Wi-Fi.
        let under = path.status == .satisfied ? usable.first(where: Self.isNetwork) ?? usable.first : nil
        interface = under?.name
        // Low Data Mode is iOS's way to mark a Wi-Fi network as metered.
        metered = path.isExpensive || path.isConstrained
        addresses = under.map { Self.routableAddresses(on: $0.name) } ?? []
    }

    private static func isNetwork(_ interface: NWInterface) -> Bool {
        let tunnel = ["utun", "ipsec", "ppp"].contains(where: interface.name.hasPrefix)
        return !tunnel && interface.type != .other
    }

    private static func routableAddresses(on name: String) -> Set<String> {
        var head: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&head) == 0, let first = head else { return [] }
        defer { freeifaddrs(head) }
        var out = Set<String>()
        for entry in sequence(first: first, next: { $0.pointee.ifa_next }) {
            guard String(cString: entry.pointee.ifa_name) == name, let address = entry.pointee.ifa_addr else { continue }
            let family = Int32(address.pointee.sa_family)
            guard family == AF_INET || family == AF_INET6 else { continue }
            var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
            guard getnameinfo(address, socklen_t(address.pointee.sa_len), &host, socklen_t(host.count), nil, 0, NI_NUMERICHOST) == 0 else { continue }
            let text = String(cString: host).lowercased()
            let linkLocal = ["fe8", "fe9", "fea", "feb"].contains(where: text.hasPrefix) || text.hasPrefix("169.254.")
            if !linkLocal { out.insert(text) }
        }
        return out
    }
}
