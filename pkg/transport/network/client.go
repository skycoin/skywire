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
	EB         *appevent.Broadcaster
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
	generic.eb = f.EB
	generic.lPK = f.PK
	generic.lSK = f.SK
	generic.listenAddr = f.ListenAddr

	switch netType {
	case STCP:
		return newStcp(generic, f.PKTable)
	case STCPR:
		return newStcpr(generic, f.ARClient)
	case SUDPH:
		return newSudph(generic, f.ARClient)
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
	netType    Type

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
	remoteAddr := conn.RemoteAddr()
	c.log.Infof("Performing handshake with %v", remoteAddr)
	hs := tphandshake.InitiatorHandshake(c.lSK, lAddr, rAddr)
	return c.wrapConn(conn, hs, true, freePort)
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

func (c *genericClient) wrapConn(conn net.Conn, hs tphandshake.Handshake, initiator bool, onClose func()) (*Conn, error) {
	lAddr, rAddr, err := hs(conn, time.Now().Add(tphandshake.Timeout))
	if err != nil {
		if err := conn.Close(); err != nil {
			c.log.WithError(err).Warnf("Failed to close connection")
		}
		onClose()
		return nil, err
	}
	c.log.Infof("Sent handshake to %v, local addr %v, remote addr %v", conn.RemoteAddr(), lAddr, rAddr)

	wrappedConn := &Conn{Conn: conn, lAddr: lAddr, rAddr: rAddr, freePort: onClose, connType: c.netType}
	err = wrappedConn.encrypt(c.lPK, c.lSK, initiator)
	if err != nil {
		return nil, err
	}
	return wrappedConn, nil
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

	onClose := func() {}
	hs := tphandshake.ResponderHandshake(tphandshake.MakeF2PortChecker(c.checkListener))
	wrappedConn, err := c.wrapConn(conn, hs, false, onClose)
	if err != nil {
		return err
	}
	lis, err := c.getListener(wrappedConn.lAddr.Port)
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

func (c *genericClient) checkListener(port uint16) error {
	_, err := c.getListener(port)
	return err
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
	lis := NewListener(lAddr, freePort, c.netType)
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

func (c *genericClient) PK() cipher.PubKey {
	return c.lPK
}

func (c *genericClient) SK() cipher.SecKey {
	return c.lSK
}

func (c *genericClient) Close() error {
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

func (c *genericClient) Type() Type {
	return c.netType
}
