import Foundation

/// The phone's pins on vpn-client: SkyDNS inside its tunnel, and the resolver
/// for ordinary names (Android: ConfigManager's VPN_APP case). The tunnel is
/// the packet-tunnel extension's (Lane D), so on the Simulator these only
/// shape the config the extension will start from.
public enum VpnProfile {
    public static let app = "vpn-client"

    /// `SkyDNS.vpnArgs`, then `DnsServer.args`, in Android's order.
    public static func phoneArgs(_ args: [String], skyDnsInVpn: Bool, dnsServer: String) -> [String] {
        DnsServer.args(SkyDNS.vpnArgs(args, on: skyDnsInVpn), server: dnsServer)
    }
}

/// SkyDNS opens .dmsg and .skynet names in every app (pkg/skydns). It rides
/// SkyVPN's tunnel when SkyVPN is on; on its own it needs a tunnel of its own,
/// which only Android's core can ask for (pkg/vpn RunSkyDNS), so the iOS
/// config carries no `skydns` app until the extension (Lane D). The port of
/// Android's core/SkyDns.kt.
public enum SkyDNS {
    /// The in-process app that runs SkyDNS without SkyVPN.
    public static let app = "skydns"

    /// The Apps hub switch: SkyDNS on its own, while SkyVPN is off.
    public static let prefStandalone = "skydns_standalone"
    public static let defaultStandalone = false

    /// The SkyVPN screen switch: SkyDNS inside SkyVPN's tunnel.
    public static let prefInVpn = "vpn_skydns"
    public static let defaultInVpn = true

    /// vpn-client's flag for SkyDNS inside its tunnel.
    public static let vpnFlag = "--mesh-gateway"

    /// vpn-client's argv with SkyDNS turned `on` or off, everything else kept.
    public static func vpnArgs(_ args: [String], on: Bool) -> [String] {
        let rest = args.filter { $0 != vpnFlag && !$0.hasPrefix("\(vpnFlag)=") }
        return on ? rest + [vpnFlag] : rest
    }

    /// Whether vpn-client's argv turns SkyDNS on.
    public static func inVpnArgs(_ args: [String]) -> Bool {
        args.contains { $0 == vpnFlag || $0 == "\(vpnFlag)=true" }
    }
}

/// The resolver SkyVPN and SkyDNS ask for every name that is not .dmsg or
/// .skynet. Both apps take it as `--dns`. With none, SkyVPN uses
/// `defaultServer`, since a VPN server has no resolver to share, and SkyDNS on
/// its own uses the network's. A phone preference, pinned on every start. The
/// port of Android's core/DnsServer.kt.
public enum DnsServer {
    /// The stored address; blank or absent means each app's own default.
    public static let prefKey = "dns_server"

    /// SkyVPN's with no `--dns`: Cloudflare's (pkg/vpn's shareDefaultDNS).
    public static let defaultServer = "1.1.1.1"

    private static let flags = ["--dns", "-dns"]

    /// IPv4 only: the tunnel and the hotspot proxy carry IPv4 (pkg/vpn/share.go).
    public static func isValid(_ address: String) -> Bool {
        let parts = address.trimmingCharacters(in: .whitespaces).split(separator: ".", omittingEmptySubsequences: false)
        return parts.count == 4 && parts.allSatisfy { part in
            (1...3).contains(part.count) && part.allSatisfy(\.isASCIIDigit) && Int(part)! <= 255
        }
    }

    /// What a stored value means for the apps: a valid address, or blank for the defaults.
    public static func sanitize(_ stored: String?) -> String {
        guard let trimmed = stored?.trimmingCharacters(in: .whitespaces), isValid(trimmed) else { return "" }
        return trimmed
    }

    /// `args` with `--dns` set to `server`, or without it when `server` is blank.
    public static func args(_ args: [String], server: String) -> [String] {
        var rest: [String] = []
        var i = 0
        while i < args.count {
            let token = args[i]
            if flags.contains(token) {
                i += 1 // and its value
            } else if !flags.contains(where: { token.hasPrefix("\($0)=") }) {
                rest.append(token)
            }
            i += 1
        }
        let wanted = server.trimmingCharacters(in: .whitespaces)
        return wanted.isEmpty ? rest : rest + [flags[0], wanted]
    }

    /// The `--dns` value in `args`, or nil when there is none.
    public static func current(_ args: [String]) -> String? {
        AppArgs.value(args, flags)
    }
}

private extension Character {
    var isASCIIDigit: Bool { isASCII && isNumber }
}
