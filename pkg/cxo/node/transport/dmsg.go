// Package transport provides CXO transport adapters.
// dmsg.go implements a CXO transport factory over DMSG connections,
// allowing CXO nodes to communicate via skywire's DMSG network
// instead of raw TCP.
package transport

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// DefaultCXOPort is the default DMSG port for CXO connections.
// Sourced from skyenv so the port allocation is visible in the same
// table as DmsgHypervisorPort (46), DmsgTransportSetupPort (47), etc.
//
// Was previously the literal 46, which clashed with DmsgHypervisorPort:
// whichever module called dmsgC.Listen first won, and on a hypervisor-
// enabled visor the CXO publisher's Listen typically fired before the
// hypervisor's, so hypervisor-RPC-over-DMSG silently failed with
// "port already occupied" and remote visors stopped showing up.
const DefaultCXOPort = skyenv.DmsgCXOPort

// DMSGFactory creates and manages CXO connections over DMSG.
// It implements the same interface as TCPFactory but uses DMSG
// streams as the underlying transport.
type DMSGFactory struct {
	AcceptedCallback func(conn *Connection)

	dmsgC *dmsg.Client
	port  uint16

	mu       sync.Mutex
	listener *dmsg.Listener
	closed   bool
}

// NewDMSGFactory creates a new DMSGFactory using the given DMSG client.
// The port parameter specifies the DMSG port to listen on and dial to.
func NewDMSGFactory(dmsgC *dmsg.Client, port uint16) *DMSGFactory {
	return &DMSGFactory{
		dmsgC: dmsgC,
		port:  port,
	}
}

// Listen starts listening on the DMSG port for incoming CXO connections.
func (f *DMSGFactory) Listen() error {
	lis, err := f.dmsgC.Listen(f.port)
	if err != nil {
		return fmt.Errorf("dmsg listen on port %d: %w", f.port, err)
	}

	f.mu.Lock()
	f.listener = lis
	f.mu.Unlock()

	go f.acceptLoop(lis)
	return nil
}

// Address returns a string representation of the listening address (PK:port).
func (f *DMSGFactory) Address() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listener != nil {
		return f.listener.Addr().String()
	}
	return ""
}

// Connect dials a remote CXO node over DMSG by public key.
func (f *DMSGFactory) Connect(remotePK cipher.PubKey) (*Connection, error) {
	addr := dmsg.Addr{PK: remotePK, Port: f.port}
	conn, err := f.dmsgC.Dial(context.Background(), addr)
	if err != nil {
		return nil, fmt.Errorf("dmsg dial %v: %w", addr, err)
	}
	return newConnection(conn, false), nil
}

// Close stops the factory and closes the listener.
func (f *DMSGFactory) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return
	}
	f.closed = true
	if f.listener != nil {
		f.listener.Close() //nolint:errcheck,gosec
	}
}

func (f *DMSGFactory) acceptLoop(lis net.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		c := newConnection(conn, false)
		if cb := f.AcceptedCallback; cb != nil {
			go cb(c)
		}
	}
}
