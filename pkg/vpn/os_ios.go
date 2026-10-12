//go:build ios

// Package vpn pkg/vpn/os_ios.go c4-app-vpn
package vpn

import (
	"errors"
	"fmt"
	"net"

	"github.com/skycoin/skywire/pkg/skydns"
)

/*
The iOS half of os_darwin.go and os_client_darwin.go, which run ifconfig,
route and networksetup: an app on iOS has none of them and cannot change the
system's interfaces or routes. The packet-tunnel extension owns the tunnel and
applies its NEPacketTunnelNetworkSettings, as Android's VpnService.Builder does
(os_client_android.go). So SetupTUN records the interface the server assigned
(TunSettings) for the extension to apply, the default route is the tunnel's
own, and the per-service /32 direct routes are the extension's excluded routes.
*/

// DefaultNetworkGateway is the unspecified address: there is no routing table to
// read, and every route call below ignores its gateway.
func DefaultNetworkGateway() (net.IP, error) {
	return net.IPv4zero, nil
}

func setupClientSysPrivileges() (int, error) {
	return 0, nil
}

func releaseClientSysPrivileges(_ int) error {
	return nil
}

// SetupTUN records the interface the server assigned for the extension to apply.
func (c *Client) SetupTUN(_, ipCIDR, gateway string, mtu int) error {
	dns := c.cfg.DNSAddr
	if c.mesh != nil {
		// SkyDNS answers inside the tunnel and asks c.cfg.DNSAddr for the rest.
		dns = skydns.ResolverAddr
	}
	s := requestTun(ipCIDR, gateway, mtu, dns)
	fmt.Printf("TUN settings %d recorded for the packet-tunnel extension: %s via %s, MTU %d\n", s.Seq, s.Addr, s.Gateway, s.MTU)
	return nil
}

// ChangeRoute is the tunnel's own default route.
func (c *Client) ChangeRoute(_, _ string) error { return nil }

// AddRoute is the tunnel's own default route, or an excluded route of the extension.
func (c *Client) AddRoute(_, _ string) error { return nil }

// DeleteRoute on a default half is the client routing traffic directly again.
func (c *Client) DeleteRoute(ip, _ string) error {
	if ip == ipv4FirstHalfAddr || ip == ipv4SecondHalfAddr {
		if s, changed := requestTunDown(); changed {
			fmt.Printf("TUN down %d recorded for the packet-tunnel extension\n", s.Seq)
		}
	}
	return nil
}

// RevertDNS is a no-op: the resolver was the tunnel's, and it goes with it.
func (c *Client) RevertDNS() {}

var errIOSNoServer = errors.New("vpn-server cannot run on iOS: an app cannot route or NAT")

// SetupTUN fails: a phone is never a VPN server.
func (s *Server) SetupTUN(_, _, _ string, _ int) error {
	return errIOSNoServer
}
