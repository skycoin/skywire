package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/skycoin/skywire/pkg/snet/directtp/porter"
	"github.com/skycoin/skywire/pkg/snet/directtp/tphandshake"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
)

type stcpClient struct {
	PK           cipher.PubKey
	SK           cipher.SecKey
	listenAddr   string
	table        stcp.PKTable
	connListener net.Listener
	// todo: change to listener wrapper type
	listeners     map[uint16]*Listener
	log           *logging.Logger
	mu            sync.RWMutex
	listenStarted chan struct{}
	done          chan struct{}
	porter        *porter.Porter
	closeOnce     sync.Once
}

func newStcp(PK cipher.PubKey, SK cipher.SecKey, addr string, table stcp.PKTable) Client {
	client := &stcpClient{PK: PK, SK: SK, listenAddr: addr, table: table}
	client.listenStarted = make(chan struct{})
	client.done = make(chan struct{})
	client.listeners = make(map[uint16]*Listener)
	return client
}

func (c *stcpClient) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (*Conn, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	c.log.Infof("Dialing PK %v", rPK)

	var conn net.Conn
	addr, ok := c.table.Addr(rPK)
	if !ok {
		return nil, fmt.Errorf("pk table: entry of %s does not exist", rPK)
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}

	c.log.Infof("Dialed %v:%v@%v", rPK, rPort, conn.RemoteAddr())

	lPort, freePort, err := c.porter.ReserveEphemeral(ctx)
	if err != nil {
		return nil, err
	}
	lAddr, rAddr := dmsg.Addr{PK: c.PK, Port: lPort}, dmsg.Addr{PK: rPK, Port: rPort}
	hs := tphandshake.InitiatorHandshake(c.SK, lAddr, rAddr)

	connConfig := ConnConfig{
		Log:       c.log,
		Conn:      conn,
		LocalSK:   c.SK,
		Deadline:  time.Now().Add(tphandshake.Timeout),
		Handshake: hs,
		FreePort:  freePort,
		Encrypt:   true,
		Initiator: true,
	}
	return NewConn(connConfig, STCP)
}

// Listen starts listening on a specified port number. The port is a skywire port
// and is not related to local OS ports. Underlying connection will most likely use
// a different port number
// Listen requires Serve to be called, which will accept connections to all skywire ports
func (c *stcpClient) Listen(port uint16) (*Listener, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	ok, freePort := c.porter.Reserve(port)
	if !ok {
		return nil, ErrPortOccupied
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	lAddr := dmsg.Addr{PK: c.PK, Port: port}
	lis := NewListener(lAddr, freePort)
	c.listeners[port] = lis

	return lis, nil
}

// LocalAddr returns local address. This is network address the client
// listens to for incoming connections, not skywire address
func (c *stcpClient) LocalAddr() (net.Addr, error) {
	<-c.listenStarted
	if c.isClosed() {
		return nil, ErrNotListening
	}
	return c.connListener.Addr(), nil
}

// Serve starts accepting all incoming connections (i.e. connections to all skywire ports)
// Connections that successfuly perform handshakes will be delivered to a listener
// bound to a specific skywire port
func (c *stcpClient) Serve() error {
	if c.connListener != nil {
		return ErrAlreadyListening
	}
	go c.serve()
	return nil
}

func (c *stcpClient) serve() {
	l, err := net.Listen("tcp", c.listenAddr)
	if err != nil {
		c.log.Errorf("Failed to listen on %q: %v", c.listenAddr, err)
		return
	}
	c.connListener = l
	close(c.listenStarted)
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

// todo: move this to generic client
func (c *stcpClient) acceptConn() error {
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
	// 1. perform handshake explicitly over conn
	// 2. wrap connection in our own connection type (for now tpconn.Conn then refactored wrapper)
	// 3. introduce wrapped connection to the listener

	var lis *Listener

	hs := tphandshake.ResponderHandshake(func(f2 tphandshake.Frame2) error {
		lis, err = c.getListener(f2.DstAddr.Port)
		return err
	})

	connConfig := ConnConfig{
		Log:       c.log,
		Conn:      conn,
		LocalPK:   c.PK,
		LocalSK:   c.SK,
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

// getListener returns listener to specified skywire port
// todo: proper listener type
// todo: move to generic client
func (c *stcpClient) getListener(port uint16) (*Listener, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	lis, ok := c.listeners[port]
	if !ok {
		return nil, errors.New("not listening on given port")
	}
	return lis, nil
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

func (c *stcpClient) isClosed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *stcpClient) Type() Type {
	return STCP
}
