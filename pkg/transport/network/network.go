// Package network pkg/transport/network/network.go
package network

import (
	"context"
	"errors"
	"net"

	"github.com/skycoin/skywire/pkg/cipher"
)

//go:generate mockery --name Dialer --case underscore --inpackage

// Dialer is an entity that can be dialed and asked for its type.
type Dialer interface {
	Dial(ctx context.Context, remote cipher.PubKey, port uint16) (net.Conn, error)
	// Probe checks whether a remote visor is reachable on the given port
	// by performing a lightweight dial and immediately closing. Returns
	// true if the dial handshake succeeded. Implementations that don't
	// support probing should return true (optimistic).
	Probe(ctx context.Context, remote cipher.PubKey, port uint16) bool
	Type() string
}

var (
	// ErrUnknownTransportType is returned when transport type is unknown.
	ErrUnknownTransportType = errors.New("unknown transport type")

	// ErrTimeout indicates a timeout.
	ErrTimeout = errors.New("timeout")

	// ErrAlreadyListening is returned when transport is already listening.
	ErrAlreadyListening = errors.New("already listening")

	// ErrNotListening is returned when transport is not listening.
	ErrNotListening = errors.New("not listening")

	// ErrPortOccupied is returned when port is occupied.
	ErrPortOccupied = errors.New("port is already occupied")
)
