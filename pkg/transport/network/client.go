// Package network client.go
package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/transport/network/handshake"
	"github.com/skycoin/skywire/pkg/transport/network/porter"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
)

// Client provides access to skywire network
// It allows dialing remote visors using their public keys, as
// well as listening to incoming transports from other visors
type Client interface {
	// Dial remote visor, that is listening on the given skywire port
	Dial(ctx context.Context, remote cipher.PubKey, port uint16) (Transport, error)
	// Start initializes the client and prepares it for listening. It is required
	// to be called to start accepting transports
	Start() error
	// Listen on the given skywire port. This can be called multiple times
	// for different ports for the same client. It requires Start to be called
	// to start accepting transports
	Listen(port uint16) (Listener, error)
	// LocalAddr returns the actual network address under which this client listens to
	// new transports
	LocalAddr() (net.Addr, error)
	// PK returns public key of the visor running this client
	PK() cipher.PubKey
	// SK returns secret key of the visor running this client
	SK() cipher.SecKey
	// Close the client, stop accepting transports. Connections returned by the
	// client should be closed manually
	Close() error
	// Type returns skywire network type in which this client operates
	Type() Type
}

// ClientFactory is used to create Client instances
// and holds dependencies for different clients
type ClientFactory struct {
	PK         cipher.PubKey
	SK         cipher.SecKey
	ListenAddr string
	PKTable    stcp.PKTable
	ARClient   addrresolver.APIClient
	EB         *appevent.Broadcaster
	DmsgC      *dmsg.Client
	MLogger    *logging.MasterLogger
}

// MakeClient creates a new client of specified type
func (f *ClientFactory) MakeClient(netType Type) (Client, error) {
	log := logging.MustGetLogger(string(netType))
	if f.MLogger != nil {
		log = f.MLogger.PackageLogger(string(netType))
	}

	p := porter.New(porter.MinEphemeral)

	generic := &genericClient{}
	generic.listenStarted = make(chan struct{})
	generic.done = make(chan struct{})
	generic.listeners = make(map[uint16]*listener)
	generic.log = log
	generic.mLog = f.MLogger
	generic.porter = p
	generic.eb = f.EB
	generic.lPK = f.PK
	generic.lSK = f.SK
	generic.listenAddr = f.ListenAddr

	resolved := &resolvedClient{genericClient: generic, ar: f.ARClient}

	switch netType {
	case STCP:
		return newStcp(generic, f.PKTable), nil
	case STCPR:
		return newStcpr(resolved), nil
	case SUDPH:
		return newSudph(resolved), nil
	case DMSG:
		return newDmsgClient(f.DmsgC), nil
	}
	return nil, fmt.Errorf("cannot initiate client, type %s not supported", netType)
}

// genericClient unites common logic for all clients
// The main responsibility is handshaking over incoming
// and outgoing raw network connections, obtaining remote information
// from the handshake and wrapping raw connections with skywire
// transport type.
// Incoming transports also directed to appropriate listener using
// skywire port, obtained from incoming transport handshake
type genericClient struct {
	lPK        cipher.PubKey
	lSK        cipher.SecKey
	listenAddr string
	netType    Type

	log    *logging.Logger
	mLog   *logging.MasterLogger
	porter *porter.Porter
	eb     *appevent.Broadcaster

	connListener  net.Listener
	listeners     map[uint16]*listener
	listenStarted chan struct{}
	mu            sync.RWMutex
	done          chan struct{}
	closeOnce     sync.Once
}

// initTransport will initialize skywire transport over opened raw connection to
// the remote client
// The process will perform handshake over raw connection
func (c *genericClient) initTransport(ctx context.Context, conn net.Conn, rPK cipher.PubKey, rPort uint16) (*transport, error) {
	lPort, freePort, err := c.porter.ReserveEphemeral(ctx)
	if err != nil {
		return nil, err
	}
	lAddr, rAddr := dmsg.Addr{PK: c.lPK, Port: lPort}, dmsg.Addr{PK: rPK, Port: rPort}
	remoteAddr := conn.RemoteAddr()
	c.log.Debugf("Performing handshake with %v", remoteAddr)
	hs := handshake.InitiatorHandshake(c.lSK, lAddr, rAddr)
	return c.wrapTransport(conn, hs, true, freePort)
}

// acceptTransports continuously accepts incoming transports that come from given listener
// these connections will be properly handshaked and passed to an appropriate skywire listener
// using skywire port
func (c *genericClient) acceptTransports(lis net.Listener) {
	c.mu.Lock()
	c.connListener = lis
	close(c.listenStarted)
	c.mu.Unlock()
	c.log.Debugf("listening on addr: %v", c.connListener.Addr())
	for {
		if err := c.acceptTransport(); err != nil {
			if errors.Is(err, io.EOF) {
				continue // likely it's a dummy connection from service discovery
			}

			if c.isClosed() && (errors.Is(err, io.ErrClosedPipe) || strings.Contains(err.Error(), "use of closed network connection")) {
				c.log.Debug("Cleanly stopped serving.")
				return
			}

			c.log.Warnf("failed to accept incoming connection: %v", err)
			if !handshake.IsHandshakeError(err) {
				c.log.Warnf("stopped serving")
				return
			}
		}
	}
}

// wrapTransport performs handshake over provided raw connection and wraps it in
// network.Transport type using the data obtained from handshake process
func (c *genericClient) wrapTransport(rawConn net.Conn, hs handshake.Handshake, initiator bool, onClose func()) (*transport, error) {
	transport, err := doHandshake(rawConn, hs, c.netType, c.log)
	if err != nil {
		onClose()
		return nil, err
	}
	transport.freePort = onClose
	c.log.Debugf("Sent handshake to %v, local addr %v, remote addr %v", rawConn.RemoteAddr(), transport.lAddr, transport.rAddr)
	if err := transport.encrypt(c.lPK, c.lSK, initiator); err != nil {
		return nil, err
	}
	return transport, nil
}

// acceptConn accepts new transport in underlying raw network listener,
// performs handshake, and using the data from the handshake wraps
// connection and delivers it to the appropriate listener.
// The listener is chosen using skywire port from the incoming visor transport
func (c *genericClient) acceptTransport() error {
	if c.isClosed() {
		return io.ErrClosedPipe
	}
	conn, err := c.connListener.Accept()
	if err != nil {
		return err
	}
	remoteAddr := conn.RemoteAddr()
	c.log.Debugf("Accepted connection from %v", remoteAddr)

	onClose := func() {}
	hs := handshake.ResponderHandshake(handshake.MakeF2PortChecker(c.checkListener))
	wrappedTransport, err := c.wrapTransport(conn, hs, false, onClose)
	if err != nil {
		return err
	}
	lis, err := c.getListener(wrappedTransport.lAddr.Port)
	if err != nil {
		return err
	}
	return lis.introduce(wrappedTransport)
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
func (c *genericClient) getListener(port uint16) (*listener, error) {
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
func (c *genericClient) Listen(port uint16) (Listener, error) {
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
	lis := newListener(lAddr, freePort, c.netType)
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

// PK implements interface
func (c *genericClient) PK() cipher.PubKey {
	return c.lPK
}

// SK implements interface
func (c *genericClient) SK() cipher.SecKey {
	return c.lSK
}

// Close implements interface
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

// Type implements interface
func (c *genericClient) Type() Type {
	return c.netType
}

// resolvedClient is a wrapper around genericClient,
// for the types of transports that use address resolver service
// to resolve addresses of remote visors
type resolvedClient struct {
	*genericClient
	ar addrresolver.APIClient
}

type dialFunc func(ctx context.Context, addr string) (net.Conn, error)

// dialVisor uses address resovler to obtain network address of the target visor
// and dials that visor address(es)
// dial process is specific to transport type and is provided by the client
func (c *resolvedClient) dialVisor(ctx context.Context, rPK cipher.PubKey, dial dialFunc) (net.Conn, error) {
	visorData, err := c.ar.Resolve(ctx, string(c.netType), rPK)
	if err != nil {
		return nil, fmt.Errorf("resolve PK: %w", err)
	}
	c.log.Debugf("Resolved PK %v to visor data %v", rPK, visorData)

	if visorData.IsLocal {
		for _, host := range visorData.Addresses {
			addr := net.JoinHostPort(host, visorData.Port)
			conn, err := dial(ctx, addr)
			if err == nil {
				return conn, nil
			}
		}
	}

	addr := visorData.RemoteAddr
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, visorData.Port)
	}
	return dial(ctx, addr)
}
