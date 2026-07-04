//go:build !tinygo

package network

import (
	"net"
)

// tuneTCPConn applies TCP_NODELAY + keepalive tuning to a raw TCP transport
// connection (no-op for non-TCP conns). Split behind a build tag: TinyGo's
// net.TCPConn lacks SetNoDelay / SetKeepAliveConfig, and a browser has no raw
// TCP carriers anyway, so tcp_tune_tinygo.go stubs this to a no-op.
func tuneTCPConn(conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true) //nolint:errcheck
		configureTCPLiveness(tcpConn)
	}
}
