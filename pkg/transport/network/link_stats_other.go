//go:build !linux && !android

// Package network pkg/transport/network/link_stats_other.go c2-net-transport
package network

import "net"

func tcpLinkStats(*net.TCPConn) *LinkStats { return nil }
