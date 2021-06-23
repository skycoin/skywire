package network

import (
	"context"
	"net"

	"github.com/skycoin/dmsg/cipher"
)

// Type is a type of network. Type affects the way connection is established
// and the data is sent
type Type string

const (
	// STCPR is a type of a transport that works via TCP and resolves addresses using address-resolver service.
	STCPR Type = "stcpr"
	// SUDPH is a type of a transport that works via UDP, resolves addresses using address-resolver service,
	// and uses UDP hole punching.
	SUDPH = "sudph"
	// STCP is a type of a transport that works via TCP and resolves addresses using PK table.
	STCP = "stcp"
	// DMSG is a type of a transport that works through an intermediary service
	DMSG = "dmsg"
)

//go:generate mockery -name Dialer -case underscore -inpkg

// Dialer is an entity that can be dialed and asked for its type.
type Dialer interface {
	Dial(ctx context.Context, remote cipher.PubKey, port uint16) (net.Conn, error)
	Type() Type
}

// Client provides access to skywire network in terms of dialing remote visors
// and listening to incoming connections
type Client interface {
	Dial(ctx context.Context, remote cipher.PubKey, port uint16) (*Conn, error)
	Listen(port uint16) (*Listener, error)
	LocalAddr() (net.Addr, error)
	Serve() error
	Close() error
	Type() Type
}

// ClientFactory is used to create Client instances
// and holds dependencies for different clients
type ClientFactory struct {
}

// MakeClient creates a new client of specified type
func (f *ClientFactory) MakeClient(ctype Type) Client {
	panic("not implemented")
}
