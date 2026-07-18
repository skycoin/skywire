// Package vpn pkg/vpn/router.go c4-app-vpn
package vpn

import (
	"fmt"
	"net"
	"strings"
)

// A Router turns a visor into a VPN gateway: it aggregates downstream LAN/WiFi
// clients on a local interface and forwards their traffic into the mesh-VPN
// tunnel that the vpn-client app maintains (with NAT), so an ordinary laptop or
// phone reaches the internet through Skywire without running any Skywire
// software itself.
//
// The router is a COMPANION to vpn-client: vpn-client owns the tunnel interface
// (tun*) to the chosen VPN server; the router owns the downstream interface plus
// the forwarding/NAT between them. Both are ordinary launcher apps; enable both
// (AutoStart) on the visor that should act as the gateway.
//
// Two variants share one code path — the only difference is whether an
// 802.11 AP is brought up on the downstream interface:
//   - Ethernet-out: LANInterface is a second wired NIC (e.g. eth1); clients plug
//     in (or sit behind a switch). Works on the existing Skyminer SBCs.
//   - WiFi-out: LANInterface is an AP-capable wireless NIC (e.g. wlan0) and WiFi
//     is set, so hostapd additionally beacons an SSID. Needs a chipset whose
//     driver does hostapd AP mode (Pi onboard, or a MT7612U USB dongle).
//
// Either way dnsmasq serves DHCP + DNS on the downstream subnet, and the
// downstream→tunnel path is masqueraded. The heavy lifting (IPv4 forwarding, NAT
// masquerade) reuses the same helpers the vpn-server app uses — see
// os_server_linux.go.

// RouterConfig is the platform-neutral configuration for a VPN router.
type RouterConfig struct {
	// LANInterface is the downstream interface that serves clients — a wired NIC
	// (e.g. "eth1") for the ethernet-out variant, or an AP-capable wireless NIC
	// (e.g. "wlan0") for the WiFi-out variant.
	LANInterface string

	// TUNInterface is the upstream mesh-VPN tunnel to masquerade into (e.g.
	// "tun0"), owned by the vpn-client app. Empty = auto-detect the first tun*
	// interface once vpn-client has brought it up.
	TUNInterface string

	// Gateway is the router's own address on the downstream subnet (the default
	// gateway handed to clients), e.g. 192.168.42.1.
	Gateway net.IP

	// Subnet is the downstream network in CIDR form, e.g. 192.168.42.0/24.
	Subnet *net.IPNet

	// DHCPStart and DHCPEnd bound the DHCP address pool. Both must lie inside
	// Subnet. Zero values default to .10 … .254 of the subnet.
	DHCPStart net.IP
	DHCPEnd   net.IP

	// DNS is the resolver advertised to clients over DHCP (e.g. 1.1.1.1). Zero
	// value defaults to the Gateway (dnsmasq then resolves for clients).
	DNS net.IP

	// LeaseTime is the DHCP lease duration in dnsmasq form (e.g. "12h"). Empty
	// defaults to "12h".
	LeaseTime string

	// WiFi, when non-nil, additionally runs hostapd to create a WiFi AP on
	// LANInterface. Nil = ethernet-out (no AP).
	WiFi *WiFiConfig
}

// WiFiConfig configures the hostapd access point for the WiFi-out variant.
type WiFiConfig struct {
	SSID string
	// Passphrase is the WPA2 pre-shared key (8–63 chars). Empty = open network
	// (allowed but strongly discouraged; validate() warns via error unless
	// explicitly opened with AllowOpen).
	Passphrase string
	// Band is "2.4" or "5". Selects hostapd hw_mode (g / a).
	Band string
	// Channel is the 802.11 channel. 0 = a sensible default for the band
	// (channel 6 on 2.4 GHz, 36 on 5 GHz).
	Channel int
	// CountryCode is the regulatory domain (e.g. "US"). Empty = "US".
	CountryCode string
	// AllowOpen permits an empty Passphrase (open network) without erroring.
	AllowOpen bool
}

// defaultLeaseTime is the DHCP lease used when RouterConfig.LeaseTime is empty.
const defaultLeaseTime = "12h"

// hwMode maps a band to a hostapd hw_mode letter.
func (w WiFiConfig) hwMode() string {
	if strings.HasPrefix(w.Band, "5") {
		return "a"
	}
	return "g"
}

// channel returns the configured channel or a per-band default.
func (w WiFiConfig) channel() int {
	if w.Channel != 0 {
		return w.Channel
	}
	if w.hwMode() == "a" {
		return 36
	}
	return 6
}

func (w WiFiConfig) countryCode() string {
	if w.CountryCode == "" {
		return "US"
	}
	return w.CountryCode
}

// withDefaults returns a copy of c with zero-value fields filled in from Subnet.
func (c RouterConfig) withDefaults() RouterConfig {
	if c.LeaseTime == "" {
		c.LeaseTime = defaultLeaseTime
	}
	if c.DNS == nil {
		c.DNS = c.Gateway
	}
	if c.Subnet != nil {
		if c.DHCPStart == nil {
			c.DHCPStart = nthHost(c.Subnet, 10)
		}
		if c.DHCPEnd == nil {
			c.DHCPEnd = nthHost(c.Subnet, 254)
		}
	}
	return c
}

// validate checks the config is internally consistent before any system state
// is touched.
func (c RouterConfig) validate() error {
	if c.LANInterface == "" {
		return fmt.Errorf("vpn-router: LAN interface is required")
	}
	if c.Gateway == nil {
		return fmt.Errorf("vpn-router: gateway IP is required")
	}
	if c.Subnet == nil {
		return fmt.Errorf("vpn-router: subnet is required")
	}
	if !c.Subnet.Contains(c.Gateway) {
		return fmt.Errorf("vpn-router: gateway %s is not inside subnet %s", c.Gateway, c.Subnet)
	}
	if c.DHCPStart != nil && !c.Subnet.Contains(c.DHCPStart) {
		return fmt.Errorf("vpn-router: DHCP start %s is not inside subnet %s", c.DHCPStart, c.Subnet)
	}
	if c.DHCPEnd != nil && !c.Subnet.Contains(c.DHCPEnd) {
		return fmt.Errorf("vpn-router: DHCP end %s is not inside subnet %s", c.DHCPEnd, c.Subnet)
	}
	if c.WiFi != nil {
		if c.WiFi.SSID == "" {
			return fmt.Errorf("vpn-router: WiFi SSID is required for the WiFi-out variant")
		}
		if c.WiFi.Passphrase == "" && !c.WiFi.AllowOpen {
			return fmt.Errorf("vpn-router: WiFi passphrase is empty; set one (8–63 chars) or explicitly allow an open network")
		}
		if l := len(c.WiFi.Passphrase); c.WiFi.Passphrase != "" && (l < 8 || l > 63) {
			return fmt.Errorf("vpn-router: WiFi passphrase must be 8–63 chars (got %d)", l)
		}
		if b := c.WiFi.Band; b != "" && !strings.HasPrefix(b, "2") && !strings.HasPrefix(b, "5") {
			return fmt.Errorf("vpn-router: WiFi band must be \"2.4\" or \"5\" (got %q)", b)
		}
	}
	return nil
}

// prefixLen returns the subnet's CIDR prefix length (e.g. 24).
func (c RouterConfig) prefixLen() int { //nolint
	if c.Subnet == nil {
		return 0
	}
	ones, _ := c.Subnet.Mask.Size()
	return ones
}

// DnsmasqConf renders the dnsmasq configuration for the downstream subnet
// (DHCP pool + the router as gateway/DNS). It is pure (no I/O) so it can be
// unit-tested and reviewed.
func (c RouterConfig) DnsmasqConf() string {
	c = c.withDefaults()
	var b strings.Builder
	fmt.Fprintf(&b, "# generated by skywire vpn-router — dnsmasq for %s\n", c.LANInterface)
	fmt.Fprintf(&b, "interface=%s\n", c.LANInterface)
	fmt.Fprintf(&b, "bind-interfaces\n")
	fmt.Fprintf(&b, "except-interface=lo\n")
	// DHCP: hand out addresses in the pool, with the router as gateway + DNS.
	fmt.Fprintf(&b, "dhcp-range=%s,%s,%s\n", c.DHCPStart, c.DHCPEnd, c.LeaseTime)
	fmt.Fprintf(&b, "dhcp-option=option:router,%s\n", c.Gateway)
	fmt.Fprintf(&b, "dhcp-option=option:dns-server,%s\n", c.DNS)
	// Upstream resolver dnsmasq forwards client DNS queries to.
	fmt.Fprintf(&b, "server=%s\n", c.DNS)
	fmt.Fprintf(&b, "domain-needed\n")
	fmt.Fprintf(&b, "bogus-priv\n")
	return b.String()
}

// HostapdConf renders the hostapd configuration for the WiFi-out variant. It is
// pure (no I/O). iface is the wireless interface the AP runs on.
func (w WiFiConfig) HostapdConf(iface string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# generated by skywire vpn-router — hostapd AP on %s\n", iface)
	fmt.Fprintf(&b, "interface=%s\n", iface)
	fmt.Fprintf(&b, "driver=nl80211\n")
	fmt.Fprintf(&b, "ssid=%s\n", w.SSID)
	fmt.Fprintf(&b, "country_code=%s\n", w.countryCode())
	fmt.Fprintf(&b, "hw_mode=%s\n", w.hwMode())
	fmt.Fprintf(&b, "channel=%d\n", w.channel())
	fmt.Fprintf(&b, "ieee80211n=1\n")
	fmt.Fprintf(&b, "wmm_enabled=1\n")
	fmt.Fprintf(&b, "auth_algs=1\n")
	fmt.Fprintf(&b, "macaddr_acl=0\n")
	if w.Passphrase == "" {
		// Open network (AllowOpen was set): no WPA block.
		fmt.Fprintf(&b, "# open network — no passphrase\n")
		return b.String()
	}
	fmt.Fprintf(&b, "wpa=2\n")
	fmt.Fprintf(&b, "wpa_key_mgmt=WPA-PSK\n")
	fmt.Fprintf(&b, "rsn_pairwise=CCMP\n")
	fmt.Fprintf(&b, "wpa_passphrase=%s\n", w.Passphrase)
	return b.String()
}

// nthHost returns the n-th host address of subnet (1-based over the host part),
// e.g. nthHost(192.168.42.0/24, 10) == 192.168.42.10. Returns nil if it would
// overflow the subnet.
func nthHost(subnet *net.IPNet, n int) net.IP {
	base := subnet.IP.To4()
	if base == nil {
		return nil
	}
	v := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
	v += uint32(n) //nolint:gosec // n is a small positive constant
	ip := net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	if !subnet.Contains(ip) {
		return nil
	}
	return ip
}
