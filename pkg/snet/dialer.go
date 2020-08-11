package snet

import (
	"context"
	"net"

	"github.com/skycoin/dmsg/cipher"
)

//go:generate mockery -name Dialer -case underscore -inpkg

// Dialer is an entity that can be dialed and asked for its type.
type Dialer interface {
	Dial(ctx context.Context, remote cipher.PubKey, port uint16) (net.Conn, error)
	Type() string
}
