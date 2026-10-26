//go:build linux || android

// Package network pkg/transport/network/tcp_notsent_linux.go c2-net-transport
package network

import (
	"net"

	"golang.org/x/sys/unix"
)

// setTCPNotSentLowat sets TCP_NOTSENT_LOWAT on c. Linux/Android only, like
// setTCPUserTimeout.
func setTCPNotSentLowat(c *net.TCPConn, n int) error {
	rc, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if cerr := rc.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_NOTSENT_LOWAT, n) //nolint:gosec // fd is a small descriptor from RawConn.Control
	}); cerr != nil {
		return cerr
	}
	return serr
}
