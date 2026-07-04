//go:build tinygo

package network

import (
	"net"
)

// tuneTCPConn is a no-op under TinyGo (no raw TCP carriers; net.TCPConn lacks the
// tuning methods). See tcp_tune_native.go.
func tuneTCPConn(net.Conn) {}
