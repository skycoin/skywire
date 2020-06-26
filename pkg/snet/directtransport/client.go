package directtransport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/AudriusButkevicius/pfilter"
	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/xtaci/kcp-go"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/arclient"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/directtransport/porter"
)

const (
	stcpType  = "stcp"
	stcprType = "stcpr"
	stcphType = "stcph"
	sudpType  = "sudp"
	sudprType = "sudpr"
	sudphType = "sudph"
)

var (
	// ErrUnknownTransportType is returned when transport type is unknown.
	ErrUnknownTransportType = errors.New("unknown transport type")
)

type ClientInterface interface { // TODO: rename
	Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (*Conn, error)
	Listen(lPort uint16) (*Listener, error)
	Serve() error
	Close() error
	Type() string
}

type ClientConfig struct {
	Type            string
	PK              cipher.PubKey
	SK              cipher.SecKey
	LocalAddr       string
	Table           PKTable
	AddressResolver arclient.APIClient
}

// Client is the central control for incoming and outgoing 'Conn's.
type Client struct {
	conf                ClientConfig
	mu                  sync.Mutex
	done                chan struct{}
	once                sync.Once
	log                 *logging.Logger
	porter              *porter.Porter
	packetFilter        *pfilter.PacketFilter
	listenerConn        net.PacketConn
	visorConn           net.PacketConn
	addressResolverConn net.PacketConn
	listener            net.Listener
	lMap                map[uint16]*Listener // key: lPort
}

// NewClient creates a net Client.
func NewClient(conf ClientConfig) *Client {
	return &Client{
		conf:   conf,
		log:    logging.MustGetLogger(conf.Type),
		porter: porter.New(porter.PorterMinEphemeral),
		lMap:   make(map[uint16]*Listener),
		done:   make(chan struct{}),
	}
}

// Serve serves the listening portion of the client.
func (c *Client) Serve() error {
	if c.listener != nil {
		return errors.New("already listening")
	}

	switch c.conf.Type {
	case stcpType, stcprType:
		listener, err := net.Listen("tcp", c.conf.LocalAddr)
		if err != nil {
			return err
		}

		c.listener = listener

	case sudpType, sudprType:
		listener, err := kcp.Listen(c.conf.LocalAddr)
		if err != nil {
			return err
		}

		c.listener = listener
	}

	c.log.Infof("listening on addr: %v", c.listener.Addr())

	// TODO(nkryuchkov): put to getDialer
	switch c.conf.Type {
	case stcprType, sudprType:
		_, port, err := net.SplitHostPort(c.listener.Addr().String())
		if err != nil {
			return err
		}

		if err := c.conf.AddressResolver.Bind(context.Background(), c.conf.Type, port); err != nil {
			return fmt.Errorf("bind %v: %w", c.conf.Type, err)
		}
	}

	go func() {
		for {
			if err := c.acceptConn(); err != nil {
				c.log.Warnf("failed to accept incoming connection: %v", err)
				if !IsHandshakeError(err) {
					c.log.Warnf("stopped serving")
					return
				}
			}
		}
	}()

	return nil
}

func (c *Client) acceptConn() error {
	if c.isClosed() {
		return io.ErrClosedPipe
	}

	conn, err := c.listener.Accept()
	if err != nil {
		return err
	}

	remoteAddr := conn.RemoteAddr()

	c.log.Infof("Accepted connection from %v", remoteAddr)

	var lis *Listener
	hs := ResponderHandshake(func(f2 Frame2) error {
		c.mu.Lock()
		defer c.mu.Unlock()

		var ok bool
		if lis, ok = c.lMap[f2.DstAddr.Port]; !ok {
			return errors.New("not listening on given port")
		}

		return nil
	})

	connConfig := ConnConfig{
		Log:       c.log,
		Conn:      conn,
		LocalPK:   c.conf.PK,
		LocalSK:   c.conf.SK,
		Deadline:  time.Now().Add(HandshakeTimeout),
		Handshake: hs,
		FreePort:  nil,
		Encrypt:   true,
		Initiator: false,
	}

	wrappedConn, err := NewConn(connConfig)
	if err != nil {
		return fmt.Errorf("newConn: %w", err)
	}

	if err := lis.Introduce(wrappedConn); err != nil {
		return fmt.Errorf("introduce: %w", err)
	}

	return nil
}

// Dial dials a new sudp.Conn to specified remote public key and port.
func (c *Client) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (*Conn, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	dial := getDialer(c.conf.Type)

	var visorConn net.Conn

	switch c.conf.Type {
	case stcpType, sudpType:
		addr, ok := c.conf.Table.Addr(rPK)
		if !ok {
			return nil, fmt.Errorf("pk table: entry of %s does not exist", rPK)
		}

		conn, err := dial(addr)
		if err != nil {
			return nil, err
		}

		visorConn = conn

	case stcprType, sudprType:
		visorData, err := c.conf.AddressResolver.Resolve(ctx, c.conf.Type, rPK)
		if err != nil {
			return nil, fmt.Errorf("resolve PK: %w", err)
		}

		conn, err := c.dialVisor(dial, visorData)
		if err != nil {
			return nil, err
		}

		visorConn = conn
	default:
		return nil, ErrUnknownTransportType
	}

	c.log.Infof("Dialed %v:%v@%v", rPK, rPort, visorConn.RemoteAddr())

	lPort, freePort, err := c.porter.ReserveEphemeral(ctx)
	if err != nil {
		return nil, err
	}

	hs := InitiatorHandshake(c.conf.SK, dmsg.Addr{PK: c.conf.PK, Port: lPort}, dmsg.Addr{PK: rPK, Port: rPort})

	connConfig := ConnConfig{
		Log:       c.log,
		Conn:      visorConn,
		LocalPK:   c.conf.PK,
		LocalSK:   c.conf.SK,
		Deadline:  time.Now().Add(HandshakeTimeout),
		Handshake: hs,
		FreePort:  freePort,
		Encrypt:   true,
		Initiator: true,
	}

	return NewConn(connConfig)
}

// TODO: same for listener
type dialFunc func(addr string) (net.Conn, error)

func getDialer(tType string) dialFunc {
	switch tType {
	case "stcp", "stcpr", "stcph":
		return func(addr string) (net.Conn, error) {
			return net.Dial("tcp", addr)
		}
	case "sudp", "sudpr", "sudph":
		return kcp.Dial
	default:
		return nil // should not happen
	}
}

func (c *Client) dialVisor(dial dialFunc, visorData arclient.VisorData) (net.Conn, error) {
	if visorData.IsLocal {
		for _, host := range visorData.Addresses {
			addr := net.JoinHostPort(host, visorData.Port)

			conn, err := dial(addr)
			if err == nil {
				return conn, nil
			}
		}
	}

	return dial(visorData.RemoteAddr)
}

// Listen creates a new listener for sudp.
// The created Listener cannot actually accept remote connections unless Serve is called beforehand.
func (c *Client) Listen(lPort uint16) (*Listener, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	ok, freePort := c.porter.Reserve(lPort)
	if !ok {
		return nil, errors.New("port is already occupied")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	lAddr := dmsg.Addr{PK: c.conf.PK, Port: lPort}
	lis := NewListener(lAddr, freePort)
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

		c.mu.Lock()
		defer c.mu.Unlock()

		if c.listener != nil {
			if err := c.listener.Close(); err != nil {
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
	return c.conf.Type
}
