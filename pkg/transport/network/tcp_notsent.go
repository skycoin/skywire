// Package network pkg/transport/network/tcp_notsent.go c2-net-transport
package network

import (
	"net"
	"sync/atomic"
)

// DefaultTCPNotSentLowat is how many unsent bytes a TCP transport lets the
// kernel hold (TCP_NOTSENT_LOWAT, tcp(7)). The rest waits in the transport's
// own write queue, so a small write is not stuck behind seconds of bulk data.
const DefaultTCPNotSentLowat = 128 << 10

var tcpNotSentLowat atomic.Int64

func init() { tcpNotSentLowat.Store(DefaultTCPNotSentLowat) }

// SetTCPNotSentLowat sets the limit for TCP transports opened from now on.
// 0 leaves the kernel's default, which holds as much as the send buffer.
func SetTCPNotSentLowat(n int) {
	if n < 0 {
		n = 0
	}
	tcpNotSentLowat.Store(int64(n))
}

// configureTCPQueue applies the TCP_NOTSENT_LOWAT limit to a TCP transport.
func configureTCPQueue(c *net.TCPConn) {
	if n := tcpNotSentLowat.Load(); n > 0 {
		_ = setTCPNotSentLowat(c, int(n)) //nolint:errcheck
	}
}
