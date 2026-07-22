// Package meshgw pkg/vpnrouter/meshgw/origdst_other.go c4-app-vpn
//go:build !linux
// +build !linux

package meshgw

import (
	"errors"
	"net"
)

// originalDst is Linux-only (SO_ORIGINAL_DST); the transparent proxy — and the
// whole vpn-router — only runs on Linux. This stub keeps the package building
// elsewhere.
func originalDst(_ *net.TCPConn) (net.IP, uint16, error) {
	return nil, 0, errors.New("meshgw: transparent proxy is only supported on Linux")
}
