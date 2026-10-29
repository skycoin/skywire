// Package netutil pkg/netutil/net.go c0-com-util
//
// The TinyGo-safe (pure) helpers. Network-interface enumeration + the
// ipinfo.io HTTP probe live in net_native.go (//go:build !tinygo); their
// TinyGo stubs are in net_tinygo.go.
package netutil

import (
	"fmt"
	"net"
	"strings"
)

// IsPublicIP returns true if the provided IP is public.
// Obtained from: https://stackoverflow.com/questions/41670155/get-public-ip-in-golang
func IsPublicIP(IP net.IP) bool {
	if IP.IsLoopback() || IP.IsLinkLocalMulticast() || IP.IsLinkLocalUnicast() {
		return false
	}
	if ip4 := IP.To4(); ip4 != nil {
		switch {
		case ip4[0] == 10:
			return false
		case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
			return false
		case ip4[0] == 192 && ip4[1] == 168:
			return false
		case ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127:
			// 100.64.0.0/10, RFC 6598 carrier-grade NAT. Not routable on the
			// public internet and not a public endpoint: a peer seen at one is
			// reached through the same carrier NAT we sit behind.
			return false
		default:
			return true
		}
	}
	return false
}

// ExtractPort returns port of the given UDP or TCP address
func ExtractPort(addr net.Addr) (uint16, error) {
	switch address := addr.(type) {
	case *net.TCPAddr:
		//nolint:gosec
		return uint16(address.Port), nil
	case *net.UDPAddr:
		//nolint:gosec
		return uint16(address.Port), nil
	default:
		return 0, fmt.Errorf("extract port: invalid address: %s", addr.String())
	}
}

// virtualIfacePrefixes names interfaces that never carry a path a peer can
// use: container bridges, veth pairs, VPN and overlay tunnels, and the
// visor's own skydns and VPN interfaces. Address resolver registration and
// the WebRTC ICE filter both read it.
var virtualIfacePrefixes = []string{
	"docker", "br-", "veth", "virbr", "lxc", "cni", "flannel", "calico",
	"skydns", "tun", "tap", "wg", "tailscale", "utun", "zt",
}

// IsVirtualInterface reports whether name is an interface whose addresses
// must not be advertised to peers (see virtualIfacePrefixes).
func IsVirtualInterface(name string) bool {
	for _, p := range virtualIfacePrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// clatNet is the IPv4 service continuity prefix a 464XLAT CLAT takes its
// address from, RFC 7335 section 4. Nothing outside the host can reach it.
var clatNet = &net.IPNet{IP: net.IPv4(192, 0, 0, 0).To4(), Mask: net.CIDRMask(29, 32)}

// IsAdvertisableIP reports whether ip may be offered to peers as a local
// address: not loopback, not link-local, and not a CLAT address.
func IsAdvertisableIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	return !clatNet.Contains(ip)
}
