package stcp

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

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/directtransport"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/directtransport/porter"
)

// Type is stcp type.
const Type = "stcp"

// Client is the central control for incoming and outgoing 'stcp.Conn's.
type Client struct {
	log *logging.Logger

	lPK cipher.PubKey
	lSK cipher.SecKey
	t   directtransport.PKTable
	p   *porter.Porter

	localAddr string
	lTCP      net.Listener
	lMap      map[uint16]*directtransport.Listener // key: lPort
	mx        sync.Mutex

	done chan struct{}
	once sync.Once
}

// NewClient creates a net Client.
func NewClient(pk cipher.PubKey, sk cipher.SecKey, t directtransport.PKTable, localAddr string) *Client {
	return &Client{
		log:       logging.MustGetLogger(Type),
		lPK:       pk,
		lSK:       sk,
		t:         t,
		p:         porter.New(porter.PorterMinEphemeral),
		localAddr: localAddr,
		lMap:      make(map[uint16]*directtransport.Listener),
		done:      make(chan struct{}),
	}
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
	c.log.Infof("listening on tcp addr: %v", lTCP.Addr())

	go func() {
		for {
			if err := c.acceptTCPConn(); err != nil {
				c.log.Warnf("failed to accept incoming connection: %v", err)
				if !directtransport.IsHandshakeError(err) {
					c.log.Warnf("stopped serving stcp")
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
		return fmt.Errorf("newConn: %w", err)
	}

	if err := lis.Introduce(conn); err != nil {
		return fmt.Errorf("introduce: %w", err)
	}

	return nil
}

// Dial dials a new stcp.Conn to specified remote public key and port.
func (c *Client) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (*directtransport.Conn, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	tcpAddr, ok := c.t.Addr(rPK)
	if !ok {
		return nil, fmt.Errorf("pk table: entry of %s does not exist", rPK)
	}

	conn, err := net.Dial("tcp", tcpAddr)
	if err != nil {
		return nil, err
	}

	c.log.Infof("Dialed %v:%v@%v", rPK, rPort, tcpAddr)

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
				c.log.WithError(err).Warnf("Failed to close stcp listener")
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
