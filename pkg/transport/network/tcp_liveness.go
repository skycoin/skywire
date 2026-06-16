package network

import (
	"net"
	"time"
)

// OS-level TCP liveness so a silently-dead peer (no RST/FIN — crash, power
// loss, NAT/hole-punch decay) is detected by the kernel in ~Idle+Interval*Count
// instead of relying on the ~3-minute app-level transport ping. Keepalive is
// portable (Go >=1.23 maps SetKeepAliveConfig to the right per-OS setsockopt:
// linux/darwin/windows/android); TCP_USER_TIMEOUT is linux/android only (see
// tcp_usertimeout_*.go) and only additionally speeds failing a pending write.
const (
	tcpKeepAliveIdle     = 15 * time.Second
	tcpKeepAliveInterval = 5 * time.Second
	tcpKeepAliveCount    = 4
	tcpUserTimeout       = 30 * time.Second
)

// configureTCPLiveness enables fast silent-death detection on a TCP transport.
func configureTCPLiveness(c *net.TCPConn) {
	_ = c.SetKeepAliveConfig(net.KeepAliveConfig{ //nolint:errcheck
		Enable:   true,
		Idle:     tcpKeepAliveIdle,
		Interval: tcpKeepAliveInterval,
		Count:    tcpKeepAliveCount,
	})
	_ = setTCPUserTimeout(c, tcpUserTimeout) //nolint:errcheck
}
