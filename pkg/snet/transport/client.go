package transport

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
	"github.com/libp2p/go-reuseport"
	"github.com/xtaci/kcp-go"

	"github.com/SkycoinProject/skywire-mainnet/internal/packetfilter"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/arclient"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/listener"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/transport/pktable"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/transport/porter"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/transport/tpconn"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/transport/tphandshake"
)

const (
	// STCPType is a type of a transport that works via TCP and resolves addresses using PK table.
	STCPType = "stcp"
	// STCPType is a type of a transport that works via TCP and resolves addresses using address-resolver service.
	STCPRType = "stcpr"
	// STCPType is a type of a transport that works via TCP, resolves addresses using address-resolver service,
	// and uses TCP hole punching.
	STCPHType = "stcph"
	// SUDPType is a type of a transport that works via UDP and resolves addresses using PK table.
	SUDPType = "sudp"
	// SUDPRType is a type of a transport that works via UDP and resolves addresses using address-resolver service.
	SUDPRType = "sudpr"
	// SUDPHType is a type of a transport that works via UDP, resolves addresses using address-resolver service,
	// and uses TCP hole punching.
	SUDPHType = "sudph"

	// HolePunchMessage is sent in a dummy UDP packet that is sent by both parties to establish UDP hole punching.
	HolePunchMessage = "holepunch"

	// DialTimeout represents a timeout for dialing.
	// TODO: Find best value.
	DialTimeout = 30 * time.Second
)

var (
	// ErrUnknownTransportType is returned when transport type is unknown.
	ErrUnknownTransportType = errors.New("unknown transport type")

	// ErrTimeout indicates a timeout.
	ErrTimeout = errors.New("timeout")

	// ErrAlreadyListening is returned when transport is already listening.
	ErrAlreadyListening = errors.New("already listening")
)

type Client interface { // TODO: rename
	Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (*tpconn.Conn, error)
	Listen(lPort uint16) (*listener.Listener, error)
	Serve() error
	Close() error
	Type() string
}

type ClientConfig struct {
	Type            string
	PK              cipher.PubKey
	SK              cipher.SecKey
	LocalAddr       string
	Table           pktable.PKTable
	AddressResolver arclient.APIClient // TODO(nkryuchkov): close it properly
}

// client is the central control for incoming and outgoing 'Conn's.
type client struct {
	conf           ClientConfig
	mu             sync.Mutex
	done           chan struct{}
	once           sync.Once
	log            *logging.Logger
	porter         *porter.Porter
	packetFilter   *pfilter.PacketFilter
	packetListener net.PacketConn
	visorConn      net.PacketConn
	stcphConnCh    <-chan arclient.RemoteVisor
	dialCh         chan cipher.PubKey
	listener       net.Listener
	listeners      map[uint16]*listener.Listener // key: lPort
}

// NewClient creates a net Client.
func NewClient(conf ClientConfig) *client {
	return &client{
		conf:      conf,
		log:       logging.MustGetLogger(conf.Type),
		porter:    porter.New(porter.MinEphemeral),
		listeners: make(map[uint16]*listener.Listener),
		done:      make(chan struct{}),
	}
}

// Serve serves the listening portion of the client.
func (c *client) Serve() error {
	switch c.conf.Type {
	case STCPType, STCPRType, SUDPType, SUDPRType:
		if c.listener != nil {
			return ErrAlreadyListening
		}
	case SUDPHType:
		if c.packetListener != nil {
			return ErrAlreadyListening
		}
	case STCPHType:
		if c.stcphConnCh != nil {
			return ErrAlreadyListening
		}
	}

	switch c.conf.Type {
	case STCPType, STCPRType:
		l, err := net.Listen("tcp", c.conf.LocalAddr)
		if err != nil {
			return err
		}

		c.listener = l

	case SUDPType, SUDPRType:
		l, err := kcp.Listen(c.conf.LocalAddr)
		if err != nil {
			return err
		}

		c.listener = l

	case SUDPHType:
		lAddr, err := net.ResolveUDPAddr("udp", "")
		if err != nil {
			return fmt.Errorf("net.ResolveUDPAddr (local): %w", err)
		}

		packetListener, err := net.ListenUDP("udp", lAddr)
		if err != nil {
			return err
		}

		c.packetListener = packetListener

		c.packetFilter = pfilter.NewPacketFilter(packetListener)
		c.visorConn = c.packetFilter.NewConn(100, nil)

		c.packetFilter.Start()

		addrCh, err := c.conf.AddressResolver.BindSUDPH(context.Background(), c.packetFilter)
		if err != nil {
			return err
		}

		go func() {
			for addr := range addrCh {
				udpAddr, err := net.ResolveUDPAddr("udp", addr.Addr)
				if err != nil {
					c.log.WithError(err).Errorf("Failed to resolve UDP address %q", addr)
					continue
				}

				// TODO(nkryuchkov): More robust solution
				c.log.Infof("Sending hole punch packet to %v", addr)
				if _, err := c.visorConn.WriteTo([]byte(HolePunchMessage), udpAddr); err != nil {
					c.log.WithError(err).Errorf("Failed to send hole punch packet to %v", udpAddr)
					continue
				}

				c.log.Infof("Sent hole punch packet to %v", addr)
			}
		}()

		listener, err := kcp.ServeConn(nil, 0, 0, c.visorConn)
		if err != nil {
			return err
		}

		c.listener = listener
	case STCPHType:
		ctx := context.Background()

		dialCh := make(chan cipher.PubKey)

		// TODO(nkryuchkov): Try to connect visors in the same local network locally.
		connCh, err := c.conf.AddressResolver.BindSTCPH(ctx, dialCh)
		if err != nil {
			return fmt.Errorf("ws: %w", err)
		}

		c.stcphConnCh = connCh
		c.dialCh = dialCh

		c.log.Infof("listening websocket events on %v", c.conf.AddressResolver.LocalTCPAddr())
	}

	if c.conf.Type != STCPHType {
		c.log.Infof("listening on addr: %v", c.listener.Addr())
	}

	// TODO(nkryuchkov): put to getDialer
	switch c.conf.Type {
	case STCPRType, SUDPRType:
		_, port, err := net.SplitHostPort(c.listener.Addr().String())
		if err != nil {
			return err
		}

		if err := c.conf.AddressResolver.Bind(context.Background(), c.conf.Type, port); err != nil {
			return fmt.Errorf("bind %v: %w", c.conf.Type, err)
		}
	}

	go func() {
		switch c.Type() {
		case STCPHType:
			for addr := range c.stcphConnCh {
				c.log.Infof("Received signal to dial %v", addr)

				go func(addr arclient.RemoteVisor) {
					if err := c.acceptSTCPHConn(addr); err != nil {
						c.log.Warnf("failed to accept incoming connection: %v", err)
					}
				}(addr)
			}

		default:
			for {
				if err := c.acceptConn(); err != nil {
					c.log.Warnf("failed to accept incoming connection: %v", err)
					if !tphandshake.IsHandshakeError(err) {
						c.log.Warnf("stopped serving")
						return
					}
				}
			}
		}
	}()

	return nil
}

func (c *client) acceptConn() error {
	if c.isClosed() {
		return io.ErrClosedPipe
	}

	conn, err := c.listener.Accept()
	if err != nil {
		return err
	}

	remoteAddr := conn.RemoteAddr()

	c.log.Infof("Accepted connection from %v", remoteAddr)

	var lis *listener.Listener
	hs := tphandshake.ResponderHandshake(func(f2 tphandshake.Frame2) error {
		c.mu.Lock()
		defer c.mu.Unlock()

		var ok bool
		if lis, ok = c.listeners[f2.DstAddr.Port]; !ok {
			return errors.New("not listening on given port")
		}

		return nil
	})

	connConfig := tpconn.Config{
		Log:       c.log,
		Conn:      conn,
		LocalPK:   c.conf.PK,
		LocalSK:   c.conf.SK,
		Deadline:  time.Now().Add(tphandshake.Timeout),
		Handshake: hs,
		FreePort:  nil,
		Encrypt:   true,
		Initiator: false,
	}

	wrappedConn, err := tpconn.NewConn(connConfig)
	if err != nil {
		return fmt.Errorf("newConn: %w", err)
	}

	if err := lis.Introduce(wrappedConn); err != nil {
		return fmt.Errorf("introduce: %w", err)
	}

	return nil
}

func (c *client) acceptSTCPHConn(remote arclient.RemoteVisor) error {
	if c.isClosed() {
		return io.ErrClosedPipe
	}

	tcpConn, err := c.dialTimeout(c.getDialer(), remote.Addr)
	if err != nil {
		return err
	}

	remoteAddr := tcpConn.RemoteAddr()

	c.log.Infof("Accepted connection from %v", remoteAddr)

	var lis *listener.Listener

	hs := tphandshake.ResponderHandshake(func(f2 tphandshake.Frame2) error {
		c.mu.Lock()
		defer c.mu.Unlock()

		var ok bool
		if lis, ok = c.listeners[f2.DstAddr.Port]; !ok {
			return errors.New("not listening on given port")
		}

		return nil
	})

	connConfig := tpconn.Config{
		Log:       c.log,
		Conn:      tcpConn,
		LocalPK:   c.conf.PK,
		LocalSK:   c.conf.SK,
		Deadline:  time.Now().Add(tphandshake.Timeout),
		Handshake: hs,
		FreePort:  nil,
		Encrypt:   true,
		Initiator: false,
	}

	conn, err := tpconn.NewConn(connConfig)
	if err != nil {
		return err
	}

	return lis.Introduce(conn)
}

// Dial dials a new sudp.Conn to specified remote public key and port.
func (c *client) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (*tpconn.Conn, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	c.log.Infof("Dialing PK %v", rPK)

	var visorConn net.Conn

	switch c.conf.Type {
	case STCPType, SUDPType:
		addr, ok := c.conf.Table.Addr(rPK)
		if !ok {
			return nil, fmt.Errorf("pk table: entry of %s does not exist", rPK)
		}

		conn, err := c.getDialer()(addr)
		if err != nil {
			return nil, err
		}

		visorConn = conn

	case STCPRType, SUDPRType:
		visorData, err := c.conf.AddressResolver.Resolve(ctx, c.conf.Type, rPK)
		if err != nil {
			return nil, fmt.Errorf("resolve PK: %w", err)
		}

		conn, err := c.dialVisor(visorData)
		if err != nil {
			return nil, err
		}

		visorConn = conn

	case SUDPHType:
		visorData, err := c.conf.AddressResolver.Resolve(ctx, c.Type(), rPK)
		if err != nil {
			return nil, fmt.Errorf("resolve PK (holepunch): %w", err)
		}

		c.log.Infof("Resolved PK %v to visor data %v, dialing", rPK, visorData)

		conn, err := c.dialVisor(visorData)
		if err != nil {
			return nil, err
		}

		visorConn = conn

	case STCPHType:
		// TODO(nkryuchkov): timeout
		c.dialCh <- rPK

		visorData, err := c.conf.AddressResolver.Resolve(ctx, c.Type(), rPK)
		if err != nil {
			return nil, fmt.Errorf("resolve PK (holepunch): %w", err)
		}

		c.log.Infof("Resolved PK %v to addr %v, dialing", rPK, visorData)

		conn, err := c.dialVisor(visorData)
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

	hs := tphandshake.InitiatorHandshake(c.conf.SK, dmsg.Addr{PK: c.conf.PK, Port: lPort}, dmsg.Addr{PK: rPK, Port: rPort})

	connConfig := tpconn.Config{
		Log:       c.log,
		Conn:      visorConn,
		LocalPK:   c.conf.PK,
		LocalSK:   c.conf.SK,
		Deadline:  time.Now().Add(tphandshake.Timeout),
		Handshake: hs,
		FreePort:  freePort,
		Encrypt:   true,
		Initiator: true,
	}

	return tpconn.NewConn(connConfig)
}

// TODO: same for listener
type dialFunc func(addr string) (net.Conn, error)

func (c *client) getDialer() dialFunc {
	switch c.conf.Type {
	case "stcp", "stcpr":
		return func(addr string) (net.Conn, error) {
			return net.Dial("tcp", addr)
		}
	case "stcph":
		return func(addr string) (net.Conn, error) {
			f := func(addr string) (net.Conn, error) {
				return reuseport.Dial("tcp", c.conf.AddressResolver.LocalTCPAddr(), addr)
			}

			return c.dialTimeout(f, addr)
		}

	case "sudp", "sudpr":
		return kcp.Dial
	case "sudph":
		return func(addr string) (net.Conn, error) {
			return c.dialTimeout(c.dialUDP, addr)
		}
	default:
		return nil // should not happen
	}
}

func (c *client) dialUDP(remoteAddr string) (net.Conn, error) {
	rAddr, err := net.ResolveUDPAddr("udp", remoteAddr)
	if err != nil {
		return nil, fmt.Errorf("net.ResolveUDPAddr (remote): %w", err)
	}

	dialConn := c.packetFilter.NewConn(20, packetfilter.NewKCPConversationFilter())

	// TODO(nkryuchkov): More robust solution
	if _, err := dialConn.WriteTo([]byte(HolePunchMessage), rAddr); err != nil {
		return nil, fmt.Errorf("dialConn.WriteTo: %w", err)
	}

	kcpConn, err := kcp.NewConn(remoteAddr, nil, 0, 0, dialConn)
	if err != nil {
		return nil, err
	}

	return kcpConn, nil
}

func (c *client) dialTimeout(dialer dialFunc, addr string) (net.Conn, error) {
	timer := time.NewTimer(DialTimeout)
	defer timer.Stop()

	c.log.Infof("Dialing %v from %v via udp", addr, c.conf.AddressResolver.LocalTCPAddr())

	for {
		select {
		case <-timer.C:
			return nil, ErrTimeout
		default:
			conn, err := dialer(addr)
			if err == nil {
				c.log.Infof("Dialed %v from %v", addr, c.conf.AddressResolver.LocalTCPAddr())
				return conn, nil
			}

			c.log.WithError(err).
				Warnf("Failed to dial %v from %v, trying again: %v", addr, c.conf.AddressResolver.LocalTCPAddr(), err)
		}
	}
}

func (c *client) dialVisor(visorData arclient.VisorData) (net.Conn, error) {
	if visorData.IsLocal {
		for _, host := range visorData.Addresses {
			addr := net.JoinHostPort(host, visorData.Port)

			conn, err := c.getDialer()(addr)
			if err == nil {
				return conn, nil
			}
		}
	}

	return c.getDialer()(visorData.RemoteAddr)
}

// Listen creates a new listener for sudp.
// The created Listener cannot actually accept remote connections unless Serve is called beforehand.
func (c *client) Listen(lPort uint16) (*listener.Listener, error) {
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
	lis := listener.NewListener(lAddr, freePort)
	c.listeners[lPort] = lis

	return lis, nil
}

// Close closes the Client.
func (c *client) Close() error {
	if c == nil {
		return nil
	}

	c.once.Do(func() {
		close(c.done)

		c.mu.Lock()
		defer c.mu.Unlock()

		if c.listener != nil {
			if err := c.listener.Close(); err != nil {
				c.log.WithError(err).Warnf("Failed to close listener")
			}
		}

		for _, lis := range c.listeners {
			if err := lis.Close(); err != nil {
				c.log.WithError(err).Warnf("Failed to close listener")
			}
		}
	})
	return nil
}

func (c *client) isClosed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// Type returns the stream type.
func (c *client) Type() string {
	return c.conf.Type
}
