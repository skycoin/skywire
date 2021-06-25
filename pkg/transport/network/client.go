package network

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/snet/arclient"
	"github.com/skycoin/skywire/pkg/snet/directtp/porter"
	"github.com/skycoin/skywire/pkg/snet/directtp/tphandshake"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
)

// Client provides access to skywire network in terms of dialing remote visors
// and listening to incoming connections
type Client interface {
	// todo: change return type to wrapped conn
	Dial(ctx context.Context, remote cipher.PubKey, port uint16) (*Conn, error)
	Listen(port uint16) (*Listener, error)
	LocalAddr() (net.Addr, error)
	PK() cipher.PubKey
	SK() cipher.SecKey
	Serve() error
	Close() error
	Type() Type
}

// ClientFactory is used to create Client instances
// and holds dependencies for different clients
type ClientFactory struct {
	PK         cipher.PubKey
	SK         cipher.SecKey
	ListenAddr string
	PKTable    stcp.PKTable
	ARClient   arclient.APIClient
	eb         *appevent.Broadcaster
}

// MakeClient creates a new client of specified type
func (f *ClientFactory) MakeClient(netType Type) Client {
	log := logging.MustGetLogger(string(netType))
	p := porter.New(porter.MinEphemeral)

	generic := &genericClient{}
	generic.listenStarted = make(chan struct{})
	generic.done = make(chan struct{})
	generic.listeners = make(map[uint16]*Listener)
	generic.log = log
	generic.porter = p
	generic.eb = f.eb
	generic.lPK = f.PK
	generic.lSK = f.SK
	generic.listenAddr = f.ListenAddr

	if netType == STCP {
		return newStcp(generic, f.PKTable)
	}
	return nil
}

// genericClient unites common logic for all clients
// The main responsibility is handshaking over incoming
// and outgoing raw network connections, obtaining remote information
// from the handshake and wrapping raw connections with skywire
// connection type.
// Incoming connections also directed to appropriate listener using
// skywire port, obtained from incoming connection handshake
type genericClient struct {
	lPK        cipher.PubKey
	lSK        cipher.SecKey
	listenAddr string

	log    *logging.Logger
	porter *porter.Porter
	eb     *appevent.Broadcaster

	connListener  net.Listener
	listeners     map[uint16]*Listener
	listenStarted chan struct{}
	mu            sync.RWMutex
	done          chan struct{}
	closeOnce     sync.Once
}

// initConnection will initialize skywire connection over opened raw connection to
// the remote client
// The process will perform handshake over raw connection
// todo: rename to handshake, initHandshake, skyConnect or smth?
func (c *genericClient) initConnection(ctx context.Context, conn net.Conn, lPK, rPK cipher.PubKey, rPort uint16) (*Conn, error) {

	lPort, freePort, err := c.porter.ReserveEphemeral(ctx)
	if err != nil {
		return nil, err
	}
	lAddr, rAddr := dmsg.Addr{PK: c.lPK, Port: lPort}, dmsg.Addr{PK: rPK, Port: rPort}
	hs := tphandshake.InitiatorHandshake(c.lSK, lAddr, rAddr)

	connConfig := ConnConfig{
		Log:       c.log,
		Conn:      conn,
		LocalPK:   c.lPK,
		LocalSK:   c.lSK,
		Deadline:  time.Now().Add(tphandshake.Timeout),
		Handshake: hs,
		FreePort:  freePort,
		Encrypt:   true,
		Initiator: true,
	}
	return NewConn(connConfig, STCP)
}

// todo: context?
// acceptConnections continuously accepts incoming connections that come from given listener
// these connections will be properly handshaked and passed to an appropriate skywire listener
// using skywire port
func (c *genericClient) acceptConnections(lis net.Listener) {
	c.mu.Lock()
	c.connListener = lis
	close(c.listenStarted)
	c.mu.Unlock()
	c.log.Infof("listening on addr: %v", c.connListener.Addr())
	for {
		if err := c.acceptConn(); err != nil {
			if errors.Is(err, io.EOF) {
				continue // likely it's a dummy connection from service discovery
			}

			c.log.Warnf("failed to accept incoming connection: %v", err)

			if !tphandshake.IsHandshakeError(err) {
				c.log.Warnf("stopped serving")
				return
			}
		}
	}
}

// todo: better comments
// acceptConn
// 1. accepts new raw network connection
// 2. performs accepting handshake over that connection
// 3. obtains skywire port from handshake
// 4. obtains skywire listener registered for that port
// 5. delivers connection to the listener
func (c *genericClient) acceptConn() error {
	if c.isClosed() {
		return io.ErrClosedPipe
	}

	conn, err := c.connListener.Accept()
	if err != nil {
		return err
	}

	remoteAddr := conn.RemoteAddr()

	c.log.Infof("Accepted connection from %v", remoteAddr)

	// todo: move handshake process out of connection wrapping.
	var lis *Listener

	hs := tphandshake.ResponderHandshake(func(f2 tphandshake.Frame2) error {
		lis, err = c.getListener(f2.DstAddr.Port)
		if err != nil {
			c.log.Errorf("cannot get listener for port %d", f2.DstAddr.Port)
		}
		return err
	})

	connConfig := ConnConfig{
		Log:       c.log,
		Conn:      conn,
		LocalPK:   c.lPK,
		LocalSK:   c.lSK,
		Deadline:  time.Now().Add(tphandshake.Timeout),
		Handshake: hs,
		FreePort:  nil,
		Encrypt:   true,
		Initiator: false,
	}

	wrappedConn, err := NewConn(connConfig, STCP)
	if err != nil {
		return err
	}

	if err := lis.Introduce(wrappedConn); err != nil {
		return err
	}

	return nil
}

// LocalAddr returns local address. This is network address the client
// listens to for incoming connections, not skywire address
func (c *genericClient) LocalAddr() (net.Addr, error) {
	<-c.listenStarted
	if c.isClosed() {
		return nil, ErrNotListening
	}
	return c.connListener.Addr(), nil
}

// getListener returns listener to specified skywire port
func (c *genericClient) getListener(port uint16) (*Listener, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	lis, ok := c.listeners[port]
	if !ok {
		return nil, errors.New("not listening on given port")
	}
	return lis, nil
}

// Listen starts listening on a specified port number. The port is a skywire port
// and is not related to local OS ports. Underlying connection will most likely use
// a different port number
// Listen requires Serve to be called, which will accept connections to all skywire ports
func (c *genericClient) Listen(port uint16) (*Listener, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	ok, freePort := c.porter.Reserve(port)
	if !ok {
		return nil, ErrPortOccupied
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	lAddr := dmsg.Addr{PK: c.lPK, Port: port}
	lis := NewListener(lAddr, freePort, STCP)
	c.listeners[port] = lis

	return lis, nil
}

func (c *genericClient) isClosed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *stcpClient) PK() cipher.PubKey {
	return c.lPK
}

func (c *stcpClient) SK() cipher.SecKey {
	return c.lSK
}

func (c *stcpClient) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)

		c.mu.Lock()
		defer c.mu.Unlock()

		if c.connListener != nil {
			if err := c.connListener.Close(); err != nil {
				c.log.WithError(err).Warnf("Failed to close incoming connection listener")
			}
		}

		for _, lis := range c.listeners {
			if err := lis.Close(); err != nil {
				c.log.WithError(err).WithField("addr", lis.Addr().String()).Warnf("Failed to close listener")
			}
		}
	})

	return nil
}
