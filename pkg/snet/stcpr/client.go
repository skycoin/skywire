package stcpr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/noise"
	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/arclient"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/noisewrapper"
)

// Type is stcp with address resolving type.
const Type = "stcpr"

// Client is the central control for incoming and outgoing 'stcp.Conn's.
type Client struct {
	log *logging.Logger

	lPK             cipher.PubKey
	lSK             cipher.SecKey
	p               *Porter
	addressResolver arclient.APIClient
	localAddr       string

	lTCP net.Listener
	lMap map[uint16]*Listener // key: lPort
	mx   sync.Mutex

	done chan struct{}
	once sync.Once
}

// NewClient creates a net Client.
func NewClient(pk cipher.PubKey, sk cipher.SecKey, addressResolver arclient.APIClient, localAddr string) *Client {
	c := &Client{
		log:             logging.MustGetLogger(Type),
		lPK:             pk,
		lSK:             sk,
		addressResolver: addressResolver,
		localAddr:       localAddr,
		p:               newPorter(PorterMinEphemeral),
		lMap:            make(map[uint16]*Listener),
		done:            make(chan struct{}),
	}

	return c
}

// SetLogger sets a logger for Client.
func (c *Client) SetLogger(log *logging.Logger) {
	c.log = log
}

// Serve serves the listening portion of the client.
func (c *Client) Serve() error {
	if c.lTCP != nil {
		return errors.New("already listening")
	}

	lTCP, err := net.Listen("tcp", c.localAddr)
	if err != nil {
		return err
	}

	c.lTCP = lTCP

	addr := lTCP.Addr()
	c.log.Infof("listening on tcp addr: %v", addr)

	_, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		port = ""
	}

	if err := c.addressResolver.Bind(context.Background(), port); err != nil {
		return fmt.Errorf("bind PK")
	}

	go func() {
		for {
			if err := c.acceptTCPConn(); err != nil {
				c.log.Warnf("failed to accept incoming connection: %v", err)

				if !IsHandshakeError(err) {
					c.log.Warnf("stopped serving stcpr")
					return
				}
			}
		}
	}()

	return nil
}

func (c *Client) acceptTCPConn() error {
	if c.isClosed() {
		return io.ErrClosedPipe
	}

	tcpConn, err := c.lTCP.Accept()
	if err != nil {
		return err
	}

	remoteAddr := tcpConn.RemoteAddr()

	c.log.Infof("Accepted connection from %v", remoteAddr)

	if appConn, isAppConn := tcpConn.(noisewrapper.PK); isAppConn {
		config := noise.Config{
			LocalPK:   c.lPK,
			LocalSK:   c.lSK,
			RemotePK:  appConn.PK(),
			Initiator: false,
		}

		tcpConn, err = noisewrapper.WrapConn(config, tcpConn)
		if err != nil {
			return fmt.Errorf("encrypt connection: %w", err)
		}

		c.log.Infof("Connection with %v is encrypted", remoteAddr)
	} else {
		c.log.Infof("Connection with %v is not encrypted", remoteAddr)
	}

	var lis *Listener

	hs := ResponderHandshake(func(f2 Frame2) error {
		c.mx.Lock()
		defer c.mx.Unlock()

		var ok bool
		if lis, ok = c.lMap[f2.DstAddr.Port]; !ok {
			return errors.New("not listening on given port")
		}

		return nil
	})

	conn, err := newConn(tcpConn, time.Now().Add(HandshakeTimeout), hs, nil)
	if err != nil {
		return err
	}

	return lis.Introduce(conn)
}

// Dial dials a new stcp.Conn to specified remote public key and port.
func (c *Client) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (*Conn, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	addr, err := c.addressResolver.Resolve(ctx, rPK)
	if err != nil {
		return nil, err
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}

	c.log.Infof("Dialed %v:%v@%v", rPK, rPort, addr)

	config := noise.Config{
		LocalPK:   c.lPK,
		LocalSK:   c.lSK,
		RemotePK:  rPK,
		Initiator: true,
	}

	conn, err = noisewrapper.WrapConn(config, conn)
	if err != nil {
		return nil, fmt.Errorf("encrypt connection: %w", err)
	}

	c.log.Infof("Connection with %v:%v@%v is encrypted", rPK, rPort, addr)

	lPort, freePort, err := c.p.ReserveEphemeral(ctx)
	if err != nil {
		return nil, err
	}

	hs := InitiatorHandshake(c.lSK, dmsg.Addr{PK: c.lPK, Port: lPort}, dmsg.Addr{PK: rPK, Port: rPort})

	return newConn(conn, time.Now().Add(HandshakeTimeout), hs, freePort)
}

// Listen creates a new listener for stcp.
// The created Listener cannot actually accept remote connections unless Serve is called beforehand.
func (c *Client) Listen(lPort uint16) (*Listener, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	ok, freePort := c.p.Reserve(lPort)
	if !ok {
		return nil, errors.New("port is already occupied")
	}

	c.mx.Lock()
	defer c.mx.Unlock()

	lAddr := dmsg.Addr{PK: c.lPK, Port: lPort}
	lis := newListener(lAddr, freePort)
	c.lMap[lPort] = lis

	return lis, nil
}

// Close closes the Client.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}

	c.once.Do(func() {
		close(c.done)

		c.mx.Lock()
		defer c.mx.Unlock()

		if c.lTCP != nil {
			_ = c.lTCP.Close() //nolint:errcheck
		}

		for _, lis := range c.lMap {
			_ = lis.Close() // nolint:errcheck
		}
	})

	return nil
}

func (c *Client) isClosed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// Type returns the stream type.
func (c *Client) Type() string {
	return Type
}
