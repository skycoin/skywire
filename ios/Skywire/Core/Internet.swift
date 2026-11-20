import CoreClient
import Foundation
import Network
import notify
import os

/// Whether the phone has a network and whether it is metered, as the system sees it (Android:
/// Internet.online and MobileData.metered). Without one the core cannot reach the Skywire network.
@MainActor
final class Internet: ObservableObject {
    /// The phone has a network with internet access. True until the first path says otherwise.
    @Published private(set) var online = true
    /// Whether the network under any VPN is metered, nil while there is none.
    @Published private(set) var metered: Bool?

    private var monitor: NWPathMonitor?

    func start() {
        guard monitor == nil else { return }
        let monitor = NWPathMonitor()
        monitor.pathUpdateHandler = { [weak self] path in
            let snapshot = NetworkPath(path)
            Task { @MainActor in self?.note(snapshot) }
        }
        monitor.start(queue: DispatchQueue(label: "com.skycoin.skywire.internet"))
        self.monitor = monitor
        #if DEBUG
        NetworkOverride.onChange { [weak self] in
            guard let self, let path = self.monitor?.currentPath else { return }
            self.note(NetworkPath(path))
        }
        #endif
    }

    func note(_ path: NetworkPath) {
        let online = path.interface != nil
        let metered = online ? path.metered : nil
        if online != self.online { self.online = online }
        if metered != self.metered { self.metered = metered }
    }
}

/// What Home says about the network under the core (Android: the network half of HomeUiState).
struct NetworkStanding: Equatable {
    /// The core is starting, running, waiting to start again or held back for Wi-Fi.
    var coreOn = false
    var connected = false
    /// The phone has a network with internet access.
    var online = true
    /// The network under the core is metered, mobile data in practice.
    var metered = false
    /// The user's MobileData choice.
    var useMobileData = MobileData.defaultAllowed
    /// The user has answered the mobile data question once.
    var asked = false

    /// On, but the phone has no way to reach the Skywire network.
    var waitingForInternet: Bool { !online && coreOn }
    /// On, but kept off the metered network the phone is using.
    var waitingForWifi: Bool { online && metered && !useMobileData && coreOn }
    var waiting: Bool { waitingForInternet || waitingForWifi }
    var onMobileData: Bool { online && metered && useMobileData && connected }
    /// Held off mobile data and never asked: the question goes on Home.
    var offerMobileData: Bool { waitingForWifi && !asked }

    /// With mobile data off, the core starts only on an unmetered network (Android:
    /// SkywireCoreService.networkAllowed). `metered` is nil while there is no network.
    static func startAllowed(metered: Bool?, useMobileData: Bool) -> Bool {
        metered == false || useMobileData
    }
}

#if DEBUG
/// A bench's stand-in for the radio a Simulator lacks: `-NetworkOverride wifi|metered|offline`
/// at launch, or the Darwin notification `com.skycoin.skywire.network.<word>` (`none` ends it).
enum NetworkOverride {
    static let words = ["wifi", "metered", "offline", "none"]
    private static let state = OSAllocatedUnfairLock<String?>(
        initialState: UserDefaults.standard.string(forKey: "NetworkOverride")
    )
    @MainActor private static var listeners: [@MainActor () -> Void] = []
    @MainActor private static var registered = false

    /// The path the override stands for, or nil when there is none.
    static var path: NetworkPath? {
        switch state.withLock({ $0 }) {
        case "wifi": NetworkPath(interface: "en0", metered: false, addresses: ["192.0.2.10"])
        case "metered": NetworkPath(interface: "pdp_ip0", metered: true, addresses: ["198.51.100.10"])
        case "offline": NetworkPath(interface: nil, metered: false, addresses: [])
        default: nil
        }
    }

    /// Calls `listener` on the main actor after each posted change.
    @MainActor static func onChange(_ listener: @escaping @MainActor () -> Void) {
        listeners.append(listener)
        guard !registered else { return }
        registered = true
        for word in words {
            var token: Int32 = 0
            notify_register_dispatch("com.skycoin.skywire.network.\(word)", &token, .main) { _ in
                state.withLock { $0 = word }
                MainActor.assumeIsolated { listeners.forEach { $0() } }
            }
        }
    }
}
#endif
