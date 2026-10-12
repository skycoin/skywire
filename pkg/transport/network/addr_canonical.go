// Package network pkg/transport/network/addr_canonical.go c2-net-transport
package network

import "net"

// canonicalAddr returns "" when raw is empty (lets the caller branch
// on "v6 unavailable"), otherwise appends port when raw is bare-host.
// Mirrors the inline check the pre-#1525 dialer did so the v4/v6
// branches stay symmetric.
func canonicalAddr(raw, port string) string {
	if raw == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(raw); err != nil {
		return net.JoinHostPort(raw, port)
	}
	return raw
}
