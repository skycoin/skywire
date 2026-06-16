//go:build !linux && !android

package network

import (
	"net"
	"time"
)

// setTCPUserTimeout is a no-op where TCP_USER_TIMEOUT doesn't exist (macOS,
// Windows, BSD). Keepalive (portable, set separately) handles silent-death
// detection on those platforms.
func setTCPUserTimeout(_ *net.TCPConn, _ time.Duration) error { return nil }
