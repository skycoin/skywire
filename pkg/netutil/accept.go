// Package netutil pkg/netutil/accept.go c0-com-util
package netutil

import (
	"errors"
	"net"
	"time"
)

// IsTransientAcceptErr reports whether an Accept error is worth retrying.
// It matches what net.OpError.Temporary reports for accept, without the
// deprecated method (https://go.dev/issue/45729).
func IsTransientAcceptErr(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	for _, errno := range transientAcceptErrnos {
		if errors.Is(err, errno) {
			return true
		}
	}
	return false
}

// AcceptBackoff paces retries of transient Accept errors the way
// net/http.Server.Serve does, from 5ms doubling up to 1s.
type AcceptBackoff struct {
	delay time.Duration
}

// Retry sleeps and returns true when err is transient. It returns false
// at once for any other error, which the caller should treat as fatal.
func (b *AcceptBackoff) Retry(err error) bool {
	if !IsTransientAcceptErr(err) {
		return false
	}
	if b.delay == 0 {
		b.delay = 5 * time.Millisecond
	} else {
		b.delay = min(b.delay*2, time.Second)
	}
	time.Sleep(b.delay)
	return true
}

// Reset clears the backoff after a successful Accept.
func (b *AcceptBackoff) Reset() { b.delay = 0 }
