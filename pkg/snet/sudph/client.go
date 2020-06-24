package sudph

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

	"github.com/SkycoinProject/skywire-mainnet/internal/packetfilter"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/arclient"
)

// Type is sudp hole punch type.
const Type = "sudph"

// DialTimeout represents a timeout for dialing.
// TODO: Find best value.
const DialTimeout = 30 * time.Second

// ErrTimeout indicates a timeout.
var ErrTimeout = errors.New("timeout")

// Client is the central control for incoming and outgoing 'sudp.Conn's.
type Client struct {
	log *logging.Logger

	lPK             cipher.PubKey
	lSK             cipher.SecKey
	p               *Porter
	addressResolver arclient.APIClient

	localUDPAddr        string
	listenerConn        net.PacketConn
	visorConn           net.PacketConn
	addressResolverConn net.PacketConn
	packetFilter        *pfilter.PacketFilter

	lUDP net.Listener
	lMap map[uint16]*Listener // key: lPort
	mx   sync.Mutex

	done chan struct{}
	once sync.Once
}

// NewClient creates a net Client.
func NewClient(pk cipher.PubKey, sk cipher.SecKey, addressResolver arclient.APIClient) *Client {
	c := &Client{
		log:             logging.MustGetLogger(Type),
		lPK:             pk,
		lSK:             sk,
		addressResolver: addressResolver,
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
	// TODO(nkryuchkov): check if already serving
	c.log.Infof("Serving SUDPH client")

	ctx := context.Background()
	network := "udp"

	lAddr, err := net.ResolveUDPAddr(network, "")
	if err != nil {
		return fmt.Errorf("net.ResolveUDPAddr (local): %w", err)
	}

	c.localUDPAddr = lAddr.String()

	log.Infof("SUDPH: Resolved local addr from %v to %v", "", lAddr)

	rAddr, err := net.ResolveUDPAddr(network, c.addressResolver.RemoteUDPAddr())
	if err != nil {
		return err
	}

	log.Infof("SUDPH dialing udp from %v to %v", lAddr, rAddr)

	listenerConn, err := net.ListenUDP(network, lAddr)
	if err != nil {
		return err
	}

	c.listenerConn = listenerConn

	c.packetFilter = pfilter.NewPacketFilter(listenerConn)
	c.visorConn = c.packetFilter.NewConn(100, nil)
	c.addressResolverConn = c.packetFilter.NewConn(10, packetfilter.NewAddressFilter(rAddr, true))

	c.packetFilter.Start()

	arKCPConn, err := kcp.NewConn(c.addressResolver.RemoteUDPAddr(), nil, 0, 0, c.addressResolverConn)
	if err != nil {
		return err
	}

	log.Infof("SUDPH updating local UDP addr from %v to %v", c.localUDPAddr, arKCPConn.LocalAddr().String())

	// TODO(nkryuchkov): consider moving some parts to address-resolver client

	emptyAddr := dmsg.Addr{PK: cipher.PubKey{}, Port: 0}
	hs := InitiatorHandshake(c.lSK, dmsg.Addr{PK: c.lPK, Port: 0}, emptyAddr)

	connConfig := ConnConfig{
		Log:       c.log,
		Conn:      arKCPConn,
		LocalPK:   c.lPK,
		LocalSK:   c.lSK,
		Deadline:  time.Now().Add(HandshakeTimeout),
		Handshake: hs,
		Encrypt:   false,
		Initiator: true,
	}

	arConn, err := NewConn(connConfig)
	if err != nil {
		return fmt.Errorf("newConn: %w", err)
	}

	// TODO(nkryuchkov): Try to connect visors in the same local network locally.
	c.addressResolver.BindSUDPH(ctx, arConn)

	lUDP, err := kcp.ServeConn(nil, 0, 0, c.visorConn)
	if err != nil {
		return err
	}

	c.lUDP = lUDP
	addr := lUDP.Addr()
	c.log.Infof("listening on udp addr: %v", addr)

	c.log.Infof("bound BindSUDPH to %v", c.addressResolver.LocalTCPAddr())

	go func() {
		for {
			if err := c.acceptUDPConn(); err != nil {
				c.log.Warnf("failed to accept incoming connection: %v", err)

				if !IsHandshakeError(err) {
					c.log.Warnf("stopped serving sudpr")
					return
				}
			}
		}
	}()

	return nil
}

func (c *Client) dialTimeout(addr string) (net.Conn, error) {
	timer := time.NewTimer(DialTimeout)
	defer timer.Stop()

	c.log.Infof("Dialing %v from %v via udp", addr, c.addressResolver.LocalTCPAddr())

	for {
		select {
		case <-timer.C:
			return nil, ErrTimeout
		default:
			conn, err := c.dialUDP(addr)
			if err == nil {
				c.log.Infof("Dialed %v from %v", addr, c.addressResolver.LocalTCPAddr())
				return conn, nil
			}

			c.log.WithError(err).
				Warnf("Failed to dial %v from %v, trying again: %v", addr, c.addressResolver.LocalTCPAddr(), err)
		}
	}
}

func (c *Client) dialUDP(remoteAddr string) (net.Conn, error) {
	log.Infof("SUDPH c.localUDPAddr: %q", c.localUDPAddr)

	lAddr, err := net.ResolveUDPAddr("udp", c.localUDPAddr)
	if err != nil {
		return nil, fmt.Errorf("net.ResolveUDPAddr (local): %w", err)
	}

	log.Infof("SUDPH: Resolved local addr from %v to %v", c.localUDPAddr, lAddr)

	log.Infof("SUDPH dialing2 udp from %v to %v", lAddr, remoteAddr)

	dialConn := c.packetFilter.NewConn(20, packetfilter.NewKCPConversationFilter())

	kcpConn, err := kcp.NewConn(remoteAddr, nil, 0, 0, dialConn)
	if err != nil {
		return nil, err
	}

	return kcpConn, nil
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

	connConfig := ConnConfig{
		Log:       c.log,
		Conn:      udpConn,
		LocalPK:   c.lPK,
		LocalSK:   c.lSK,
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

// Dial dials a new sudph.Conn to specified remote public key and port.
func (c *Client) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (*Conn, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	c.log.Infof("Dialing PK %v", rPK)

	addr, err := c.addressResolver.ResolveSUDPH(ctx, rPK)
	if err != nil {
		return nil, fmt.Errorf("resolve PK (holepunch): %w", err)
	}

	c.log.Infof("Resolved PK %v to addr %v, dialing", rPK, addr)

	udpConn, err := c.dialTimeout(addr)
	if err != nil {
		return nil, err
	}

	c.log.Infof("Dialed %v:%v@%v", rPK, rPort, addr)

	lPort, freePort, err := c.p.ReserveEphemeral(ctx)
	if err != nil {
		return nil, fmt.Errorf("ReserveEphemeral: %w", err)
	}

	hs := InitiatorHandshake(c.lSK, dmsg.Addr{PK: c.lPK, Port: lPort}, dmsg.Addr{PK: rPK, Port: rPort})

	connConfig := ConnConfig{
		Log:       c.log,
		Conn:      udpConn,
		LocalPK:   c.lPK,
		LocalSK:   c.lSK,
		Deadline:  time.Now().Add(HandshakeTimeout),
		Handshake: hs,
		FreePort:  freePort,
		Encrypt:   true,
		Initiator: true,
	}

	sudpConn, err := NewConn(connConfig)
	if err != nil {
		return nil, fmt.Errorf("newConn: %w", err)
	}

	return sudpConn, nil
}

// Listen creates a new listener for sudp hole punch.
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

		if err := c.addressResolver.Close(); err != nil {
			c.log.WithError(err).Warnf("Failed to close address resolver client")
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
