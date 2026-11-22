@testable import CoreClient
import NetworkExtension
import XCTest

/// The tunnel the extension applies for each state of vpn-client, from the
/// JSON skywire_tun_settings returns (Go's pkg/vpn/tun_settings.go).
final class TunnelSettingsTests: XCTestCase {
    private let up = Data(#"{"up":true,"addr":"192.168.255.6/29","gateway":"192.168.255.5","mtu":1500,"dns":"1.1.1.1","seq":3}"#.utf8)

    func testDecodesWhatGoWrites() throws {
        let tun = try JSONDecoder().decode(TunSettings.self, from: up)
        XCTAssertEqual(tun, TunSettings(up: true, addr: "192.168.255.6/29", gateway: "192.168.255.5", mtu: 1500, dns: "1.1.1.1", seq: 3))
        // Go leaves the empty fields out when the tunnel is down.
        let down = try JSONDecoder().decode(TunSettings.self, from: Data(#"{"up":false,"seq":4}"#.utf8))
        XCTAssertEqual(down, TunSettings(seq: 4))
    }

    func testTheMaskComesFromThePrefix() {
        XCTAssertEqual(TunSettings(addr: "192.168.255.6/29").ipv4AddressAndMask?.mask, "255.255.255.248")
        XCTAssertEqual(TunSettings(addr: "10.0.0.2/32").ipv4AddressAndMask?.mask, "255.255.255.255")
        XCTAssertEqual(TunSettings(addr: "10.0.0.2/0").ipv4AddressAndMask?.mask, "0.0.0.0")
        XCTAssertNil(TunSettings(addr: "10.0.0.2").ipv4AddressAndMask)
        XCTAssertNil(TunSettings(addr: "fd00::2/64").ipv4AddressAndMask)
    }

    /// SkyVPN on: the server's address, every IPv4 route, the MTU, and its resolver for every name.
    func testSkyVPNRoutesEverythingThroughTheTunnel() throws {
        let settings = try XCTUnwrap(TunnelNetworkSettings.vpn(try JSONDecoder().decode(TunSettings.self, from: up)))
        XCTAssertEqual(settings.tunnelRemoteAddress, "192.168.255.5")
        let ipv4 = try XCTUnwrap(settings.ipv4Settings)
        XCTAssertEqual(ipv4.addresses, ["192.168.255.6"])
        XCTAssertEqual(ipv4.subnetMasks, ["255.255.255.248"])
        XCTAssertEqual(ipv4.includedRoutes?.map(\.destinationAddress), [NEIPv4Route.default().destinationAddress])
        XCTAssertEqual(settings.mtu, 1500)
        XCTAssertEqual(settings.dnsSettings?.servers, ["1.1.1.1"])
        XCTAssertEqual(settings.dnsSettings?.matchDomains, [""])
    }

    /// Connected with SkyVPN off: the tunnel carries nothing.
    func testIdleRoutesNothing() throws {
        let ipv4 = try XCTUnwrap(TunnelNetworkSettings.idle().ipv4Settings)
        XCTAssertEqual(ipv4.addresses, [TunnelNetworkSettings.idleAddress])
        XCTAssertEqual(ipv4.includedRoutes?.count, 0)
        XCTAssertNil(TunnelNetworkSettings.idle().dnsSettings)
    }

    func testADownTunnelHasNoVPNSettings() {
        XCTAssertNil(TunnelNetworkSettings.vpn(TunSettings(seq: 4)))
        XCTAssertNil(TunnelNetworkSettings.vpn(TunSettings(up: true, addr: "not an address", seq: 5)))
    }
}
