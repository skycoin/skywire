//go:build darwin || linux

// Package netutil pkg/netutil/net_default_route.go c0-com-util
//
// Finding the default interface without running anything, for iOS, which has
// no shell to run route(8) with (net_ios.go). Built for macOS and Linux too, so
// the tests can hold it against route(8) and ip(8) where those exist.
package netutil

import (
	"fmt"
	"net"

	"github.com/wlynxg/anet"
)

// defaultRouteProbe is where the kernel is asked to route to. Any address the
// default route covers will do (a documentation address, never a real host):
// connecting a UDP socket only picks the route and the source address, and
// sends nothing.
const defaultRouteProbe = "192.0.2.1:9"

// interfaceOfDefaultRoute names the interface the default route leaves by:
// the one holding the source address the kernel picks for a destination only
// the default route covers. That is what `route -n get default` reports on
// macOS. "" when there is no default route.
func interfaceOfDefaultRoute() (string, error) {
	conn, err := net.Dial("udp4", defaultRouteProbe)
	if err != nil {
		return "", nil // no IPv4 route at all: unknown, as the callers expect
	}
	source := conn.LocalAddr().(*net.UDPAddr).IP
	_ = conn.Close() //nolint:errcheck // nothing was sent; closing cannot lose anything

	ifaces, err := anet.Interfaces()
	if err != nil {
		return "", fmt.Errorf("error getting network interfaces: %w", err)
	}
	for _, iface := range ifaces {
		addrs, err := anet.InterfaceAddrsByInterface(&iface)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.Equal(source) {
				return iface.Name, nil
			}
		}
	}
	return "", nil
}
