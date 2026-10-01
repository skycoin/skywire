//go:build tinygo

// Package network pkg/transport/network/accept_closed_tinygo.go c2-net-transport
package network

import (
	"errors"
	"io"
	"net"
)

// listenerGone: as accept_closed.go, without cmux, which TinyGo builds do
// not demultiplex through.
func listenerGone(err error) bool {
	return errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed)
}
