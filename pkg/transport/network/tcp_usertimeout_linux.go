//go:build linux || android

package network

import (
	"net"
	"time"

	"golang.org/x/sys/unix"
)

// setTCPUserTimeout sets TCP_USER_TIMEOUT so a write to a silently-dead peer
// errors in ~d instead of after minutes of kernel retransmits. Linux/Android
// only (Android runs the Linux kernel; note GOOS=android != linux in Go, hence
// the explicit build tag).
func setTCPUserTimeout(c *net.TCPConn, d time.Duration) error {
	rc, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if cerr := rc.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, int(d.Milliseconds()))
	}); cerr != nil {
		return cerr
	}
	return serr
}
