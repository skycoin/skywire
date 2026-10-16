//go:build !tinygo

// Package network pkg/transport/network/accept_closed.go c2-net-transport
package network

import (
	"errors"
	"io"
	"net"

	"github.com/soheilhy/cmux"
)

// listenerGone reports whether an Accept error means the listener is closed
// for good: the standard library's ways of saying so, and cmux's, since the
// stcpr listener is one arm of the shared transport port's demultiplexer
// (tcpdemux.go). Once cmux is closed every Accept fails at once with
// "mux: server closed" or "mux: listener closed"; an accept loop that took
// those for passing errors retried a dead listener in a busy loop.
func listenerGone(err error) bool {
	return errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, cmux.ErrServerClosed) || errors.Is(err, cmux.ErrListenerClosed)
}
