//go:build !tinygo

// Package dmsg pkg/dmsg/tcpnodelay_native.go
package dmsg

import (
	"net"
)

// setTCPNoDelay disables Nagle's algorithm on a TCP conn (TCP_NODELAY). dmsg
// multiplexes many small logical streams (pty keystrokes, dmsg-HTTP, RPC) over
// one TCP conn, where Nagle adds 40–200ms of per-write latency. TinyGo's
// net.TCPConn has no SetNoDelay method, so this is a no-op there
// (tcpnodelay_tinygo.go) — a TinyGo client's transport is rarely raw TCP anyway.
func setTCPNoDelay(c *net.TCPConn) { _ = c.SetNoDelay(true) } //nolint:errcheck
