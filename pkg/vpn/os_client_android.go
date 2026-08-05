//go:build android
// +build android

// Package vpn pkg/vpn/os_client_android.go c4-app-vpn
package vpn

import (
	"errors"
	"net"
)

/*
The Android half of os_client_linux.go.

None of what that file does is available here — an app process holds no
CAP_NET_ADMIN, cannot open a netlink route socket, and has no `ip` or `nmcli`
to shell out to. It also does not need any of it: the phone's TUN comes from
VpnService, whose Builder declares the interface address, the MTU, the DNS
servers and the routes, and the system installs them itself when the interface
is established (see tun_device_android.go).

So these are not stubs standing in for something that failed — they are the
same intentions expressed the only way Android allows:

  - SetupTUN is the whole configuration in one call: it establishes the
    interface with the address the server just assigned.
  - AddRoute/ChangeRoute are already satisfied. The default route was declared
    at establish time, and the per-service /32 direct routes are unnecessary:
    the app excludes its own UID from the tunnel wholesale
    (addDisallowedApplication), so the visor's dmsg traffic never entered it.
  - DeleteRoute on the default route is the shared client saying "stop carrying
    traffic" — with the killswitch off, on a dropped tunnel. Here the interface
    IS the route, so the interface goes away and the phone falls back to its
    normal networking. With the killswitch on this is never called until the
    app stops, which is exactly what makes the block real: the interface stays
    up with nobody reading it, and packets go nowhere.
  - DNS travels with the interface, so there is nothing to set or revert.
*/

// DefaultNetworkGateway fetches system's default network gateway.
//
// There is no readable routing table here and nothing to do with the answer:
// every route call below ignores its gateway argument. NewClient requires one,
// so it gets the unspecified address rather than a value that would imply the
// phone's real gateway had been discovered.
func DefaultNetworkGateway() (net.IP, error) {
	return net.IPv4zero, nil
}

// setupClientSysPrivileges is a no-op: an Android app is never privileged, and
// the operations that wanted CAP_NET_ADMIN are all gone from this build.
func setupClientSysPrivileges() (int, error) {
	return 0, nil
}

func releaseClientSysPrivileges(_ int) error {
	return nil
}

// SetupTUN establishes the interface with the parameters the VPN server
// assigned. `ifcName` is ignored: the system names the interface, and the name
// this side reports is cosmetic.
func (c *Client) SetupTUN(_, ipCIDR, gateway string, mtu int) error {
	// Called from setupTUN with tunMu held, so c.tun is stable here — and it
	// is the device newTUNDevice built, which on Android is only ever this
	// type. A vpn-SERVER would land here with the same TUN, which is why the
	// phone's config has no vpn-server: it could not route or NAT anyway.
	tun, ok := c.tun.(*androidTUN)
	if !ok {
		return errors.New("SetupTUN: the TUN did not come from the Android VPN service")
	}

	return tun.establish(ipCIDR, gateway, mtu, c.cfg.DNSAddr)
}

// ChangeRoute is declared by VpnService.Builder at establish time.
func (c *Client) ChangeRoute(_, _ string) error { return nil }

// AddRoute is declared by VpnService.Builder at establish time.
func (c *Client) AddRoute(_, _ string) error { return nil }

// DeleteRoute drops the interface when the route being removed is the default
// one, which is the shared client routing traffic directly again.
func (c *Client) DeleteRoute(ip, _ string) error {
	if ip != ipv4FirstHalfAddr && ip != ipv4SecondHalfAddr {
		// A /32 direct route to a skywire service — never installed here.
		return nil
	}

	tun := c.androidTUN()
	if tun == nil {
		return nil
	}

	return tun.down()
}

// RevertDNS is a no-op: the resolver was the interface's, and it goes with it.
func (c *Client) RevertDNS() {}

// androidTUN returns the device under the lock that guards it. Unlike
// SetupTUN, DeleteRoute is reached from routeTrafficDirectly, which does not
// hold tunMu.
func (c *Client) androidTUN() *androidTUN {
	c.tunMu.Lock()
	defer c.tunMu.Unlock()

	tun, _ := c.tun.(*androidTUN)

	return tun
}
