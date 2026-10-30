//go:build linux || ios

// Package netutil pkg/netutil/net_first_routable.go c0-com-util
package netutil

import (
	"fmt"
	"net"

	"github.com/wlynxg/anet"
)

// firstRoutableInterface is the fallback when the default route cannot be
// read: the first interface that is up, not loopback, not a container bridge,
// and holds a global IPv4 address. "" when none qualifies (airplane mode),
// without an error: the historical contract of DefaultNetworkInterface, which
// callers handle as "unknown" rather than failing a summary.
func firstRoutableInterface() (string, error) {
	ifaces, err := anet.Interfaces()
	if err != nil {
		return "", fmt.Errorf("error getting network interfaces: %w", err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if IsVirtualInterface(iface.Name) {
			continue
		}
		addrs, err := anet.InterfaceAddrsByInterface(&iface)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && ip.To4() != nil && ip.IsGlobalUnicast() {
				return iface.Name, nil
			}
		}
	}
	return "", nil
}
