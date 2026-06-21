// Package netutil pkg/netutil/net.go
//
// The TinyGo-safe (pure) helpers. Network-interface enumeration + the
// ipinfo.io HTTP probe live in net_native.go (//go:build !tinygo); their
// TinyGo stubs are in net_tinygo.go.
package netutil

import (
	"fmt"
	"net"
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

// IsVirtualInterface returns true for Docker bridges, veth pairs, and other
// virtual interfaces that shouldn't be registered with the address resolver.
func IsVirtualInterface(name string) bool {
	prefixes := []string{"docker", "br-", "veth", "virbr", "lxc", "cni", "flannel", "calico"}
	for _, p := range prefixes {
		if len(name) >= len(p) && name[:len(p)] == p {
			return true
		}
	}
	return false
}
