//go:build unix

// Package skysocks pkg/skysocks/listen_unix.go c4-app-proxy
package skysocks

import (
	"context"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// ReuseListen binds a TCP listener with SO_REUSEADDR + SO_REUSEPORT so the
// skysocks-client can rebind its SOCKS port without racing the previous
// socket's TIME_WAIT.
//
// The client rebinds :<addr> twice on EVERY --reconnect cycle: the sessionless
// "disconnected" listener that serves the branded interstitial while dialing,
// then the live Client's own listener once the exit is up. Any tunnel-down
// event (a dead mux leg, an exit restart) ends a cycle and starts a new one, so
// the port is reopened constantly under normal operation. With a plain
// net.Listen the reopen could hit "bind: address already in use" (the just-
// closed socket still in TIME_WAIT) and DROP the app — the exact fail-the-app
// lifecycle bug. SO_REUSEADDR lets the bind reclaim a TIME_WAIT socket;
// SO_REUSEPORT covers the brief within-cycle window where the disconnected and
// live listeners momentarily both hold the addr during handoff.
func ReuseListen(addr string) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			var cerr error
			if err := c.Control(func(fd uintptr) {
				if e := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); e != nil {
					cerr = e
					return
				}
				cerr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
			}); err != nil {
				return err
			}
			return cerr
		},
	}
	return lc.Listen(context.Background(), "tcp", addr)
}
