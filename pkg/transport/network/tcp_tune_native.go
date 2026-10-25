//go:build !(tinygo && js)

// Package network pkg/transport/network/tcp_tune_native.go c2-net-transport
package network

import (
	"net"

	"github.com/soheilhy/cmux"
)

// tuneTCPConn applies TCP_NODELAY, keepalive and TCP_NOTSENT_LOWAT tuning to a
// raw TCP transport connection (no-op for non-TCP conns). Split behind a build
// tag: TinyGo's net.TCPConn lacks SetNoDelay / SetKeepAliveConfig, and a browser
// has no raw TCP carriers anyway, so tcp_tune_tinygo.go stubs this to a no-op.
func tuneTCPConn(conn net.Conn) {
	if tcpConn, ok := unwrapTCPConn(conn); ok {
		_ = tcpConn.SetNoDelay(true) //nolint:errcheck
		configureTCPLiveness(tcpConn)
		configureTCPQueue(tcpConn)
	}
}

// unwrapTCPConn finds the TCP connection under conn. A connection accepted on
// the shared transport port arrives wrapped by cmux, and asserting
// *net.TCPConn on it left those transports without any of this tuning.
func unwrapTCPConn(conn net.Conn) (*net.TCPConn, bool) {
	if mc, ok := conn.(*cmux.MuxConn); ok {
		conn = mc.Conn
	}
	tc, ok := conn.(*net.TCPConn)
	return tc, ok
}

func tcpConnLinkStats(conn net.Conn) *LinkStats {
	if tc, ok := unwrapTCPConn(conn); ok {
		return tcpLinkStats(tc)
	}
	return nil
}
