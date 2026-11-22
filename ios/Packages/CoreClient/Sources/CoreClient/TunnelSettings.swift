import Foundation
#if canImport(NetworkExtension)
import NetworkExtension
#endif

/// The tunnel vpn-client asks for (skywire_tun_settings; Go's
/// pkg/vpn/tun_settings.go): the address and gateway the server assigned, the
/// MTU and the resolver. The packet-tunnel extension applies it.
public struct TunSettings: Codable, Sendable, Equatable {
    public var up: Bool
    /// CIDR, `192.168.255.6/29`.
    public var addr: String
    public var gateway: String
    public var mtu: Int
    public var dns: String
    /// Grows with every change.
    public var seq: UInt64

    enum CodingKeys: String, CodingKey { case up, addr, gateway, mtu, dns, seq }

    public init(up: Bool = false, addr: String = "", gateway: String = "", mtu: Int = 0, dns: String = "", seq: UInt64 = 0) {
        self.up = up
        self.addr = addr
        self.gateway = gateway
        self.mtu = mtu
        self.dns = dns
        self.seq = seq
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        up = c.lenient(.up, false)
        addr = c.lenient(.addr, "")
        gateway = c.lenient(.gateway, "")
        mtu = c.lenient(.mtu, 0)
        dns = c.lenient(.dns, "")
        seq = c.lenient(.seq, 0)
    }

    /// The address and its dotted mask, from `addr`; nil when it is not IPv4 CIDR.
    public var ipv4AddressAndMask: (address: String, mask: String)? {
        let parts = addr.split(separator: "/")
        guard parts.count == 2, let bits = Int(parts[1]), (0...32).contains(bits),
              parts[0].split(separator: ".").count == 4
        else { return nil }
        let mask = bits == 0 ? UInt32(0) : UInt32.max << (32 - bits)
        let octets = [24, 16, 8, 0].map { String((mask >> UInt32($0)) & 0xff) }
        return (String(parts[0]), octets.joined(separator: "."))
    }
}

#if canImport(NetworkExtension)
/// The extension's NEPacketTunnelNetworkSettings for each state of the visor.
public enum TunnelNetworkSettings {
    /// The tunnel's address while SkyVPN is off: one host route nobody uses
    /// (RFC 2544's benchmarking range), so the tunnel carries nothing.
    public static let idleAddress = "198.18.0.1"

    /// Connected with SkyVPN off: the tunnel is up so the visor runs in the
    /// background, and routes nothing (proposal §5.2).
    public static func idle() -> NEPacketTunnelNetworkSettings {
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "127.0.0.1")
        let ipv4 = NEIPv4Settings(addresses: [idleAddress], subnetMasks: ["255.255.255.255"])
        ipv4.includedRoutes = []
        settings.ipv4Settings = ipv4
        return settings
    }

    /// SkyVPN on: every IPv4 route through the tunnel, with the address the
    /// server assigned and its resolver. Traffic of the extension itself (the
    /// visor's transports) never enters its own tunnel. IPv6 is not carried
    /// yet (vpn-client assigns IPv4 only). Nil when `tun` is down or unreadable:
    /// then `idle()` applies.
    public static func vpn(_ tun: TunSettings) -> NEPacketTunnelNetworkSettings? {
        guard tun.up, let (address, mask) = tun.ipv4AddressAndMask else { return nil }
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: tun.gateway.isEmpty ? "127.0.0.1" : tun.gateway)
        let ipv4 = NEIPv4Settings(addresses: [address], subnetMasks: [mask])
        ipv4.includedRoutes = [NEIPv4Route.default()]
        settings.ipv4Settings = ipv4
        if tun.mtu > 0 {
            settings.mtu = NSNumber(value: tun.mtu)
        }
        if !tun.dns.isEmpty {
            let dns = NEDNSSettings(servers: [tun.dns])
            // Every name, not only some domains.
            dns.matchDomains = [""]
            settings.dnsSettings = dns
        }
        return settings
    }
}
#endif
