// Package netutil pkg/netutil/lan.go c0-com-util
package netutil

import (
	"net"

	"github.com/wlynxg/anet"
)

// LocalNetworks are the private networks this host is on: its RFC 1918 and
// unique local addresses with their masks, on up interfaces that are not
// loopback or virtual. A VPN tunnel is left out, as its peers are not here.
func LocalNetworks() []*net.IPNet {
	ifaces, err := anet.Interfaces()
	if err != nil {
		return nil
	}
	var nets []*net.IPNet
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 || IsVirtualInterface(iface.Name) {
			continue
		}
		addrs, err := anet.InterfaceAddrsByInterface(&iface)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.IsPrivate() {
				nets = append(nets, n)
			}
		}
	}
	return nets
}

// OnNetworks returns the addresses in hosts that are private and inside one
// of nets: a peer's LAN addresses that this host can reach directly.
func OnNetworks(hosts []string, nets []*net.IPNet) []string {
	var out []string
	for _, h := range hosts {
		ip := net.ParseIP(h)
		if ip == nil || !ip.IsPrivate() {
			continue
		}
		for _, n := range nets {
			if n.Contains(ip) {
				out = append(out, h)
				break
			}
		}
	}
	return out
}
