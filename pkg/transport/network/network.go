package network

import (
	"context"
	"errors"
	"net"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skywire/pkg/snet/arclient"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
)

// Type is a type of network. Type affects the way connection is established
// and the way data is sent
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

// skywire address consisting of pulic key, port and
// type of transport we connect over
type addr struct {
	PK   cipher.PubKey
	Port uint16
	Type Type
}

// Network name, e.g. stcpr
func (a addr) Network() string {
	return string(a.Type)
}

// String form of address
func (a addr) String() string {
	// use dmsg.Addr for printing. This address doesn't have
	// to be dmsg though
	dmsgAddr := dmsg.Addr{PK: a.PK, Port: a.Port}
	return dmsgAddr.String()
}

//go:generate mockery -name Dialer -case underscore -inpkg

// Dialer is an entity that can be dialed and asked for its type.
type Dialer interface {
	Dial(ctx context.Context, remote cipher.PubKey, port uint16) (net.Conn, error)
	Type() Type
}

// Client provides access to skywire network in terms of dialing remote visors
// and listening to incoming connections
type Client interface {
	// todo: change return type to wrapped conn
	Dial(ctx context.Context, remote cipher.PubKey, port uint16) (*Conn, error)
	Listen(port uint16) (*Listener, error)
	LocalAddr() (net.Addr, error)
	Serve() error
	Close() error
	Type() Type
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

// ClientFactory is used to create Client instances
// and holds dependencies for different clients
type ClientFactory struct {
	PK         cipher.PubKey
	SK         cipher.SecKey
	ListenAddr string
	PKTable    stcp.PKTable
	ARClient   arclient.APIClient
}

// MakeClient creates a new client of specified type
func (f *ClientFactory) MakeClient() Client {
	return newStcp(f.PK, f.SK, f.ListenAddr, f.PKTable)
}
