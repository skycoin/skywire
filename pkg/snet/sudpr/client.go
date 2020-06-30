package sudpr

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
	"github.com/xtaci/kcp-go"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/arclient"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/directtransport"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/directtransport/porter"
)

// Type is sudpr type.
const Type = "sudpr"

// Client is the central control for incoming and outgoing 'sudp.Conn's.
type Client struct {
	log *logging.Logger

	lPK             cipher.PubKey
	lSK             cipher.SecKey
	p               *porter.Porter
	addressResolver arclient.APIClient
	localAddr       string

	lUDP net.Listener
	lMap map[uint16]*directtransport.Listener // key: lPort
	mx   sync.Mutex

	done chan struct{}
	once sync.Once
}

// NewClient creates a net Client.
func NewClient(pk cipher.PubKey, sk cipher.SecKey, addressResolver arclient.APIClient, localAddr string) *Client {
	return &Client{
		log:             logging.MustGetLogger(Type),
		lPK:             pk,
		lSK:             sk,
		p:               porter.New(porter.PorterMinEphemeral),
		addressResolver: addressResolver,
		localAddr:       localAddr,
		lMap:            make(map[uint16]*directtransport.Listener),
		done:            make(chan struct{}),
	}
}

// SetLogger sets a logger for Client.
func (c *Client) SetLogger(log *logging.Logger) {
	c.log = log
}

// Serve serves the listening portion of the client.
func (c *Client) Serve() error {
	if c.lUDP != nil {
		return errors.New("already listening")
	}

	lUDP, err := kcp.Listen(c.localAddr)
	if err != nil {
		return err
	}

	c.lUDP = lUDP
	addr := lUDP.Addr()
	c.log.Infof("listening on udp addr: %v", addr)

	_, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		port = ""
	}

	if err := c.addressResolver.BindSUDPR(context.Background(), port); err != nil {
		return fmt.Errorf("bind SUDPR: %w", err)
	}

	go func() {
		for {
			if err := c.acceptUDPConn(); err != nil {
				c.log.Warnf("failed to accept incoming connection: %v", err)

				if !directtransport.IsHandshakeError(err) {
					c.log.Warnf("stopped serving sudpr")
					return
				}
			}
		}
	}()

	return nil
}

func (c *Client) acceptUDPConn() error {
	if c.isClosed() {
		return io.ErrClosedPipe
	}

	udpConn, err := c.lUDP.Accept()
	if err != nil {
		return err
	}

	remoteAddr := udpConn.RemoteAddr()

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
		Conn:      udpConn,
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

// Dial dials a new sudpr.Conn to specified remote public key and port.
func (c *Client) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (*directtransport.Conn, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	visorData, err := c.addressResolver.ResolveSUDPR(ctx, rPK)
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
			conn, err := kcp.Dial(addr)
			if err == nil {
				return conn, nil
			}
		}
	}

	return kcp.Dial(visorData.RemoteAddr)
}

// Listen creates a new listener for sudp.
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

		if c.lUDP != nil {
			if err := c.lUDP.Close(); err != nil {
				c.log.WithError(err).Warnf("Failed to close UDP listener")
			}
		}

		for _, lis := range c.lMap {
			if err := lis.Close(); err != nil {
				c.log.WithError(err).Warnf("Failed to close sudp listener")
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
