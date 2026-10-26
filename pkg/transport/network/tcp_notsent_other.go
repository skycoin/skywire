//go:build !linux && !android

// Package network pkg/transport/network/tcp_notsent_other.go c2-net-transport
package network

import "net"

// setTCPNotSentLowat is a no-op off Linux and Android, where it is not tested.
func setTCPNotSentLowat(_ *net.TCPConn, _ int) error { return nil }
