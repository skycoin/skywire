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
	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/arclient"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/directtransport"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/directtransport/porter"
)

// Type is stcp with address resolving type.
const Type = "stcpr"

// Client is the central control for incoming and outgoing 'stcp.Conn's.
type Client struct {
	log *logging.Logger

	lPK             cipher.PubKey
	lSK             cipher.SecKey
	p               *porter.Porter
	addressResolver arclient.APIClient
	localAddr       string

	lTCP net.Listener
	lMap map[uint16]*directtransport.Listener // key: lPort
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
		p:               porter.New(porter.PorterMinEphemeral),
		lMap:            make(map[uint16]*directtransport.Listener),
		done:            make(chan struct{}),
	}

	return c
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

	if err := c.addressResolver.BindSTCPR(context.Background(), port); err != nil {
		return fmt.Errorf("bind STCPR: %w", err)
	}

	go func() {
		for {
			if err := c.acceptTCPConn(); err != nil {
				c.log.Warnf("failed to accept incoming connection: %v", err)

				if !directtransport.IsHandshakeError(err) {
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

	var lis *directtransport.Listener

	hs := directtransport.ResponderHandshake(func(f2 directtransport.Frame2) error {
		c.mx.Lock()
		defer c.mx.Unlock()

		var ok bool
		if lis, ok = c.lMap[f2.DstAddr.Port]; !ok {
			return errors.New("not listening on given port")
		}

		return nil
	})

	connConfig := directtransport.ConnConfig{
		Log:       c.log,
		Conn:      tcpConn,
		LocalPK:   c.lPK,
		LocalSK:   c.lSK,
		Deadline:  time.Now().Add(directtransport.HandshakeTimeout),
		Handshake: hs,
		FreePort:  nil,
		Encrypt:   true,
		Initiator: false,
	}

	conn, err := directtransport.NewConn(connConfig)
	if err != nil {
		return err
	}

	return lis.Introduce(conn)
}

// Dial dials a new stcp.Conn to specified remote public key and port.
func (c *Client) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (*directtransport.Conn, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	visorData, err := c.addressResolver.ResolveSTCPR(ctx, rPK)
	if err != nil {
		return nil, fmt.Errorf("resolve PK: %w", err)
	}

	conn, err := c.dialVisor(visorData)
	if err != nil {
		return nil, err
	}

	c.log.Infof("Dialed %v:%v@%v", rPK, rPort, conn.RemoteAddr())

	lPort, freePort, err := c.p.ReserveEphemeral(ctx)
	if err != nil {
		return nil, err
	}

	hs := directtransport.InitiatorHandshake(c.lSK, dmsg.Addr{PK: c.lPK, Port: lPort}, dmsg.Addr{PK: rPK, Port: rPort})

	connConfig := directtransport.ConnConfig{
		Log:       c.log,
		Conn:      conn,
		LocalPK:   c.lPK,
		LocalSK:   c.lSK,
		Deadline:  time.Now().Add(directtransport.HandshakeTimeout),
		Handshake: hs,
		FreePort:  freePort,
		Encrypt:   true,
		Initiator: true,
	}

	return directtransport.NewConn(connConfig)
}

func (c *Client) dialVisor(visorData arclient.VisorData) (net.Conn, error) {
	if visorData.IsLocal {
		for _, host := range visorData.Addresses {
			addr := net.JoinHostPort(host, visorData.Port)
			conn, err := net.Dial("tcp", addr)
			if err == nil {
				return conn, nil
			}
		}
	}

	return net.Dial("tcp", visorData.RemoteAddr)
}

// Listen creates a new listener for stcp.
// The created Listener cannot actually accept remote connections unless Serve is called beforehand.
func (c *Client) Listen(lPort uint16) (*directtransport.Listener, error) {
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
	lis := directtransport.NewListener(lAddr, freePort)
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
			if err := c.lTCP.Close(); err != nil {
				c.log.WithError(err).Warnf("Failed to close TCP listener")
			}
		}

		for _, lis := range c.lMap {
			if err := lis.Close(); err != nil {
				c.log.WithError(err).Warnf("Failed to close stcpr listener")
			}
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
