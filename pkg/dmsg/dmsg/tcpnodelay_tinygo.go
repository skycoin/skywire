//go:build tinygo

// Package dmsg pkg/dmsg/dmsg/tcpnodelay_tinygo.go c1-net-dmsg
package dmsg

import (
	"net"
)

// setTCPNoDelay is a no-op under TinyGo: net.TCPConn has no SetNoDelay method.
// See tcpnodelay_native.go.
func setTCPNoDelay(_ *net.TCPConn) {}
