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
	"github.com/libp2p/go-reuseport"
	"github.com/xtaci/kcp-go"

	"github.com/SkycoinProject/skywire-mainnet/internal/packetfilter"
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
	AddressResolver arclient.APIClient // TODO(nkryuchkov): close it properly
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
	connCh              <-chan arclient.RemoteVisor
	dialCh              chan cipher.PubKey
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
	switch c.conf.Type {
	case stcpType, stcprType, sudpType, sudprType:
		if c.listener != nil {
			return ErrAlreadyListening
		}
	case sudphType:
		if c.listenerConn != nil {
			return ErrAlreadyListening
		}
	case stcphType:
		if c.connCh != nil {
			return ErrAlreadyListening
		}
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

	case sudphType:
		ctx := context.Background()
		network := "udp"

		lAddr, err := net.ResolveUDPAddr(network, "")
		if err != nil {
			return fmt.Errorf("net.ResolveUDPAddr (local): %w", err)
		}

		c.conf.LocalAddr = lAddr.String() // TODO(nkryuchkov): remove?

		c.log.Infof("SUDPH: Resolved local addr from %v to %v", "", lAddr)

		rAddr, err := net.ResolveUDPAddr(network, c.conf.AddressResolver.RemoteUDPAddr())
		if err != nil {
			return err
		}

		c.log.Infof("SUDPH dialing udp from %v to %v", lAddr, rAddr)

		listenerConn, err := net.ListenUDP(network, lAddr)
		if err != nil {
			return err
		}

		c.listenerConn = listenerConn

		c.packetFilter = pfilter.NewPacketFilter(listenerConn)
		c.visorConn = c.packetFilter.NewConn(100, nil)
		c.addressResolverConn = c.packetFilter.NewConn(10, packetfilter.NewAddressFilter(rAddr))

		c.packetFilter.Start()

		_, localPort, err := net.SplitHostPort(c.addressResolverConn.LocalAddr().String())
		if err != nil {
			return err
		}

		c.log.Infof("SUDPH Local port: %v", localPort)

		arKCPConn, err := kcp.NewConn(c.conf.AddressResolver.RemoteUDPAddr(), nil, 0, 0, c.addressResolverConn)
		if err != nil {
			return err
		}

		c.log.Infof("SUDPH updating local UDP addr from %v to %v", c.conf.LocalAddr, arKCPConn.LocalAddr().String())

		// TODO(nkryuchkov): consider moving some parts to address-resolver client
		emptyAddr := dmsg.Addr{PK: cipher.PubKey{}, Port: 0}
		hs := InitiatorHandshake(c.conf.SK, dmsg.Addr{PK: c.conf.PK, Port: 0}, emptyAddr)

		connConfig := ConnConfig{
			Log:       c.log,
			Conn:      arKCPConn,
			LocalPK:   c.conf.PK,
			LocalSK:   c.conf.SK,
			Deadline:  time.Now().Add(HandshakeTimeout),
			Handshake: hs,
			Encrypt:   false,
			Initiator: true,
		}

		arConn, err := NewConn(connConfig)
		if err != nil {
			return fmt.Errorf("newConn: %w", err)
		}

		addrCh, err := c.conf.AddressResolver.BindSUDPH(ctx, arConn, localPort)
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
	case stcphType:
		ctx := context.Background()

		dialCh := make(chan cipher.PubKey)

		// TODO(nkryuchkov): Try to connect visors in the same local network locally.
		connCh, err := c.conf.AddressResolver.BindSTCPH(ctx, dialCh)
		if err != nil {
			return fmt.Errorf("ws: %w", err)
		}

		c.connCh = connCh
		c.dialCh = dialCh

		c.log.Infof("listening websocket events on %v", c.conf.AddressResolver.LocalTCPAddr())
	}

	if c.conf.Type != stcphType {
		c.log.Infof("listening on addr: %v", c.listener.Addr())
	}

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
		switch c.Type() {
		case stcphType:
			for addr := range c.connCh {
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
					if !IsHandshakeError(err) {
						c.log.Warnf("stopped serving")
						return
					}
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

func (c *Client) acceptSTCPHConn(remote arclient.RemoteVisor) error {
	if c.isClosed() {
		return io.ErrClosedPipe
	}

	tcpConn, err := c.dialTimeout(c.getDialer(), remote.Addr)
	if err != nil {
		return err
	}

	remoteAddr := tcpConn.RemoteAddr()

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
		Conn:      tcpConn,
		LocalPK:   c.conf.PK,
		LocalSK:   c.conf.SK,
		Deadline:  time.Now().Add(HandshakeTimeout),
		Handshake: hs,
		FreePort:  nil,
		Encrypt:   true,
		Initiator: false,
	}

	conn, err := NewConn(connConfig)
	if err != nil {
		return err
	}

	return lis.Introduce(conn)
}

// Dial dials a new sudp.Conn to specified remote public key and port.
func (c *Client) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (*Conn, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	c.log.Infof("Dialing PK %v", rPK)

	var visorConn net.Conn

	switch c.conf.Type {
	case stcpType, sudpType:
		addr, ok := c.conf.Table.Addr(rPK)
		if !ok {
			return nil, fmt.Errorf("pk table: entry of %s does not exist", rPK)
		}

		conn, err := c.getDialer()(addr)
		if err != nil {
			return nil, err
		}

		visorConn = conn

	case stcprType, sudprType:
		visorData, err := c.conf.AddressResolver.Resolve(ctx, c.conf.Type, rPK)
		if err != nil {
			return nil, fmt.Errorf("resolve PK: %w", err)
		}

		conn, err := c.dialVisor(visorData)
		if err != nil {
			return nil, err
		}

		visorConn = conn

	case sudphType:
		visorData, err := c.conf.AddressResolver.ResolveSUDPH(ctx, rPK)
		if err != nil {
			return nil, fmt.Errorf("resolve PK (holepunch): %w", err)
		}

		c.log.Infof("Resolved PK %v to visor data %v, dialing", rPK, visorData)

		conn, err := c.dialVisor(visorData)
		if err != nil {
			return nil, err
		}

		visorConn = conn

	case stcphType:
		// TODO(nkryuchkov): timeout
		c.dialCh <- rPK

		visorData, err := c.conf.AddressResolver.ResolveSTCPH(ctx, rPK)
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

func (c *Client) getDialer() dialFunc {
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
			// TODO(nkryuchkov): Consider using c.dialTimeout for all transport types.
			return c.dialTimeout(c.dialUDP, addr)
		}
	default:
		return nil // should not happen
	}
}

func (c *Client) dialUDP(remoteAddr string) (net.Conn, error) {
	c.log.Infof("SUDPH c.localUDPAddr: %q", c.conf.LocalAddr)

	// TODO(nkryuchkov): Dial using listener conn?
	lAddr, err := net.ResolveUDPAddr("udp", c.conf.LocalAddr)
	if err != nil {
		return nil, fmt.Errorf("net.ResolveUDPAddr (local): %w", err)
	}

	rAddr, err := net.ResolveUDPAddr("udp", remoteAddr)
	if err != nil {
		return nil, fmt.Errorf("net.ResolveUDPAddr (remote): %w", err)
	}

	c.log.Infof("SUDPH: Resolved local addr from %v to %v", c.conf.LocalAddr, lAddr)

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

func (c *Client) dialTimeout(dialer dialFunc, addr string) (net.Conn, error) {
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

func (c *Client) dialVisor(visorData arclient.VisorData) (net.Conn, error) {
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
