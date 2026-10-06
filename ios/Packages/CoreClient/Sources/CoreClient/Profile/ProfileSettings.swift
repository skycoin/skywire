import Foundation

/// Which transport type the visor reaches for first, every other type left
/// behind it as a fallback. One visor-wide order decides both which type is
/// created when a route needs a transport and which existing one a route
/// rides. The port of Android's core/TransportPreference.kt.
public enum TransportPreference {
    /// Relayed through a dmsg server; always reachable, no NAT to beat.
    public static let dmsg = "dmsg"
    /// Direct TCP, the peer's address from the address resolver.
    public static let stcpr = "stcpr"
    /// Direct UDP with hole punching; needs a STUN-friendly NAT.
    public static let sudph = "sudph"

    /// The types offered as the primary: the three the visor's
    /// transport-creation path can establish on demand for an outgoing dial.
    public static let choices = [dmsg, stcpr, sudph]

    /// dmsg, on a phone: behind carrier NAT stcpr almost never establishes
    /// and sudph needs a NAT type carriers rarely give, yet the core's own
    /// order tries both first and spends up to ~20 s before reaching dmsg.
    public static let defaultPrimary = dmsg

    /// The core's default order, kept whole so that hoisting a primary out of
    /// it keeps everything else in its relative order.
    static let coreOrder = ["stcpr", "squicr", "sudph", "stcp", "webrtc", "swsr", "swtr", "dmsg"]

    /// The full order to hand the visor, `primary` in front.
    public static func order(primary: String) -> [String] {
        [primary] + coreOrder.filter { $0 != primary }
    }

    /// A stored or unknown value mapped onto a supported choice.
    public static func sanitize(_ value: String?) -> String {
        guard let value, choices.contains(value) else { return defaultPrimary }
        return value
    }
}

/// How much the visor logs: the config's top-level `log_level`. The port of
/// Android's core/CoreLogLevel.kt.
public enum CoreLogLevel {
    /// What `config gen` writes; quieter than the visor's own fallback for an
    /// empty field (debug), which is a lot of writing for a log nobody reads.
    public static let defaultLevel = "info"
    /// Coarse to fine. fatal and panic parse too but are not offered.
    public static let levels = ["error", "warn", "info", "debug", "trace"]

    public static func sanitize(_ stored: String?) -> String {
        guard let level = stored?.lowercased(), levels.contains(level) else { return defaultLevel }
        return level
    }
}

/// The remote-management grant: one public key, the only thing that lets a
/// machine other than this phone drive its visor. It goes into the config's
/// `hypervisors` list, which is both halves of remote access: the visor dials
/// out to that key as its hypervisor, and the key is admitted inbound on the
/// dmsg RPC surfaces (`skywire cli --via dmsg://<phone>`). The phone runs no
/// dmsgpty and pins dmsgscp off, so the key gets the typed API only. The port
/// of Android's core/RemoteManagement.kt.
public enum RemoteManagement {
    /// 33 bytes of compressed secp256k1 public key, as hex.
    public static let pkHexLength = 66

    /// `raw` as the lowercase hex the config carries, or nil when it is not
    /// shaped like a visor key. Shape only: real validation is key handling,
    /// which stays in the core.
    public static func sanitize(_ raw: String?) -> String? {
        guard let pk = raw?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased(),
              pk.count == pkHexLength,
              pk.allSatisfy({ $0.isASCII && $0.isHexDigit })
        else { return nil }
        return pk
    }
}

/// What the user decides about the core, applied to the config on every
/// start (the visor reads each once, while it builds its modules, so a change
/// means a core restart).
public struct ProfileSettings: Sendable, Equatable {
    /// The transport type tried first (`TransportPreference`).
    public var transportPrimary: String
    /// Fleet: hands the local API the dmsg client, which opens the listener
    /// other visors dial in on (`hypervisor.dmsg_ingest`). The only listener
    /// the outside world can reach, so off until asked for.
    public var fleetEnabled: Bool
    /// Automatic transports to every public visor
    /// (`transport.public_autoconnect`). Off: on mobile data they cost
    /// battery and a radio that never idles, mostly for the network's
    /// benefit.
    public var publicAutoconnect: Bool
    /// `log_level` (`CoreLogLevel`).
    public var logLevel: String
    /// The remote-management grant, or nil for none (`RemoteManagement`).
    public var remoteManagementPK: String?
    /// The Go memory limit (`memory_limit`), iOS only; nil writes no limit.
    public var memoryLimit: String?
    /// SkyDNS inside SkyVPN's tunnel (`SkyDNS.prefInVpn`).
    public var skyDnsInVpn: Bool
    /// The resolver for ordinary names, blank for each app's default (`DnsServer`).
    public var dnsServer: String

    /// The Go memory limit the iOS core runs under (playbook item 1.7): the
    /// packet-tunnel extension's budget is about 50 MB of phys_footprint, and
    /// the app uses the extension's value so the Simulator predicts the
    /// device. Not "auto": that reads /proc/meminfo, which iOS lacks.
    public static let iosMemoryLimit = "40MiB"

    public init(
        transportPrimary: String = TransportPreference.defaultPrimary,
        fleetEnabled: Bool = false,
        publicAutoconnect: Bool = false,
        logLevel: String = CoreLogLevel.defaultLevel,
        remoteManagementPK: String? = nil,
        memoryLimit: String? = ProfileSettings.iosMemoryLimit,
        skyDnsInVpn: Bool = SkyDNS.defaultInVpn,
        dnsServer: String = ""
    ) {
        self.transportPrimary = transportPrimary
        self.fleetEnabled = fleetEnabled
        self.publicAutoconnect = publicAutoconnect
        self.logLevel = logLevel
        self.remoteManagementPK = remoteManagementPK
        self.memoryLimit = memoryLimit
        self.skyDnsInVpn = skyDnsInVpn
        self.dnsServer = dnsServer
    }
}
