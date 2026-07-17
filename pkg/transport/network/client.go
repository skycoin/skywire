// Package network pkg/transport/network/client.go c2-net-transport
package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network/handshake"
	"github.com/skycoin/skywire/pkg/transport/network/porter"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// tcpDialAnnouncer is the interface satisfied by *appevent.Broadcaster that the TCP carriers
// (stcp/stcpr) use to emit TCPDial app-events. Declared as a local interface so
// this file (and the dmsg/stcp carriers that compile under TinyGo) does not
// import pkg/app/appevent — which pulls net/rpc, broken on the TinyGo target.
// *appevent.Broadcaster satisfies it; the visor passes it through ClientFactory.EB.
type tcpDialAnnouncer interface {
	SendTCPDial(ctx context.Context, remoteNet, remoteAddr string)
}

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
	Type() types.Type
}

// ClientFactory is used to create Client instances
// and holds dependencies for different clients
type ClientFactory struct {
	PK         cipher.PubKey
	SK         cipher.SecKey
	ListenAddr string
	PKTable    stcp.PKTable
	// WSTable maps a peer PK to its direct WebSocket (wss://) URL for the WS
	// transport type. Reuses stcp.PKTable (PK→URL); empty/absent disables WS.
	WSTable stcp.PKTable
	// WTTable maps a peer PK to its direct WebTransport endpoint + pinned cert
	// hash for the WT transport type. A nil/absent table disables WT dialing.
	WTTable WTTable
	// QUICTable statically pins a peer PK → UDP address ("host:port") for the QUIC
	// transport type (the QUIC analog of PKTable/stcp). Consulted before the
	// address resolver; nil/absent = AR-only.
	QUICTable stcp.PKTable
	// ICEURLs are the STUN/TURN URLs used for ICE by the WEBRTC transport type
	// (skywire's own STUN, reused from sudph). Empty = host candidates only.
	ICEURLs []string
	// ARClient is the address-resolver client (addrresolver.APIClient). Typed as
	// `any` so this file doesn't import addrresolver — which pulls net/http +
	// packetfilter (→ quic-go), none of which compile on the TinyGo target. Only
	// the AR-resolved carriers (stcpr/sudph/quic, all //go:build !tinygo) consume
	// it, via a type-assert in makeResolvedClient (client_resolved.go). The visor
	// passes a real addrresolver.APIClient; a nil/absent one is fine for a
	// dmsg-only (e.g. browser) client.
	ARClient any
	EB       tcpDialAnnouncer
	DmsgC    *dmsg.Client
	MLogger  *logging.MasterLogger
	// OnExternalSTCPR is called when an incoming STCPR connection is detected
	// from an external (non-LAN) IP address. This validates that the visor is
	// reachable from the internet.
	OnExternalSTCPR func()

	// udpDemux holds a *udpDemux (the unified transport_port shared UDP socket)
	// when EnableUnifiedUDP has been called; nil otherwise → per-type UDP binding.
	// Typed `any` because udpDemux is //go:build !tinygo (quic-go/kcp-go), and this
	// struct is in the untagged file; the !tinygo client_unified_udp.go manages it.
	udpDemux any
	// sharedQUIC holds a *sharedQUICMux when EnableUnifiedUDP has been called: the
	// single quic.Transport over the demux's QUIC conn that multiplexes squicr + WT
	// (by ALPN) onto transport_port. `any` for the same tinygo-tag reason as
	// udpDemux; consumed by the !tinygo quic/WT serve paths.
	sharedQUIC any
	// tcpDemux holds a *tcpDemux (the unified transport_port shared TCP listener,
	// cmux) when EnableUnifiedTCP has been called. `any` for the same reason as
	// udpDemux; managed by the !tinygo client_unified_tcp.go.
	tcpDemux any
	// stcprSharedListener / wsSharedListener are the tcpDemux's per-protocol
	// virtual listeners, set by EnableUnifiedTCP. net.Listener (stdlib), so the
	// WS case in MakeClient (untagged) can read wsSharedListener directly.
	stcprSharedListener net.Listener
	wsSharedListener    net.Listener
	// tcpDefaultDemux is true when the TCP cmux is the DEFAULT one bound on the
	// stcpr port (EnableDefaultTCPDemux) rather than an explicit transport_port
	// master. In that case stcpr always rides the cmux (the demux IS its port), so
	// stcprSharedListenerFor ignores the per-type break-out check.
	tcpDefaultDemux bool
}

// MakeClient creates a new client of specified type
func (f *ClientFactory) MakeClient(netType types.Type, port int) (Client, error) {
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
	generic.onExternalSTCPR = f.OnExternalSTCPR

	switch netType {
	case types.STCP:
		return newStcp(generic, f.PKTable), nil
	case types.WS:
		wc := newWS(generic, f.WSTable)
		wc.(*wsClient).ar = f.ARClient // native: resolve ws:// from the stcpr AR record
		if f.wsSharedListener != nil { // unified transport port
			wc.(*wsClient).sharedListener = f.wsSharedListener
		}
		return wc, nil
	case types.WT:
		wc := newWT(generic, f.WTTable)
		wtc := wc.(*wtClient)
		wtc.ar = f.ARClient // native: register/resolve WT endpoint+cert via AR
		// WT is HTTP/3-over-QUIC and can't share squicr's QUIC server on one socket
		// directly, but it CAN ride the SAME unified UDP socket via ALPN: when
		// wt_port is 0 and a transport_port is configured, WT registers its "h3"
		// handler on the shared QUIC mux (sharedQUICMux) so it serves on
		// transport_port — reachable through the operator's single port-forward.
		// A non-zero wt_port (or no unified socket) keeps WT on its own UDP port
		// (f.ListenAddr is the TCP/STCP addr, never used for WT).
		wtc.listenAddr = fmt.Sprintf(":%d", port)
		if port == 0 {
			wtc.sharedQUIC = f.sharedQUIC // nil unless EnableUnifiedUDP ran
		}
		return wc, nil
	case types.WEBRTC:
		return newWebRTC(generic, f.DmsgC, f.ICEURLs), nil
	case types.DMSG:
		return newDmsgClient(f.DmsgC), nil
	}
	// STCPR / SUDPH / QUIC all ride the address resolver + raw UDP/TCP — none of
	// which exist on the TinyGo/browser target. makeResolvedClient is build-tagged
	// (real on native in client_resolved.go; an unsupported-error stub under
	// //go:build tinygo), so this file stays addrresolver-free.
	return f.makeResolvedClient(netType, generic, port)
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
	netType    types.Type

	log    *logging.Logger
	mLog   *logging.MasterLogger
	porter *porter.Porter
	eb     tcpDialAnnouncer

	connListener    net.Listener
	listeners       map[uint16]*listener
	listenStarted   chan struct{}
	mu              sync.RWMutex
	done            chan struct{}
	closeOnce       sync.Once
	onExternalSTCPR func() // called when external STCPR connection received
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
			if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "encrypt connection to") {
				c.log.Debugf("Ignoring likely scanner/dummy connection: %v", err)
				continue // likely it's a dummy connection from service discovery or port scanner
			}

			if c.isClosed() && (errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed)) {
				c.log.Debug("Cleanly stopped serving.")
				return
			}

			c.log.Warnf("failed to accept incoming connection: %v", err)
			if !handshake.IsHandshakeError(err) {
				c.log.Warnf("non-handshake accept error, continuing: %v", err)
				continue
			}
		}
	}
}

// wrapTransport performs handshake over provided raw connection and wraps it in
// network.Transport type using the data obtained from handshake process
func (c *genericClient) wrapTransport(rawConn net.Conn, hs handshake.Handshake, initiator bool, onClose func()) (*transport, error) {
	transport, err := doHandshake(rawConn, hs, c.netType, handshake.Timeout, c.log)
	if err != nil {
		onClose()
		return nil, err
	}
	transport.freePort = onClose
	c.log.Debugf("Sent handshake to %v, local addr %v, remote addr %v", rawConn.RemoteAddr(), transport.lAddr, transport.rAddr)
	if err := transport.encrypt(c.lPK, c.lSK, initiator); err != nil {
		transport.Close() //nolint:errcheck,gosec
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
	// TCP_NODELAY on inbound TCP transports (stcpr/stcp) so the
	// accepting end doesn't Nagle-batch responses to small
	// interactive payloads (per-keystroke skypty bytes, small RPC
	// replies). The dial side sets the same flag in stcpr.go /
	// stcp.go; without matching it here, the lag re-appears on the
	// reverse direction of any interactive stream. UDP-based
	// sudph isn't affected (no Nagle); the type assertion is a
	// no-op for non-TCP listeners.
	tuneTCPConn(conn)
	remoteAddr := conn.RemoteAddr()
	c.log.Debugf("Accepted connection from %v", remoteAddr)

	// Check for external STCPR connection (for public visor validation)
	if c.netType == types.STCPR && c.onExternalSTCPR != nil {
		if tcpAddr, ok := remoteAddr.(*net.TCPAddr); ok {
			if !isPrivateIP(tcpAddr.IP) {
				c.log.Debugf("Detected external STCPR connection from %v", tcpAddr.IP)
				c.onExternalSTCPR()
			}
		}
	}

	onClose := func() {}
	hs := handshake.ResponderHandshake(handshake.MakeF2PortChecker(c.checkListener))
	wrappedTransport, err := c.wrapTransport(conn, hs, false, onClose)
	if err != nil {
		conn.Close() //nolint:errcheck,gosec
		return err
	}

	lis, err := c.getListener(wrappedTransport.lAddr.Port)
	if err != nil {
		wrappedTransport.Close() //nolint:errcheck,gosec
		return err
	}

	// If introduce fails (e.g., timeout or listener closed), the transport is already closed by introduce()
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
func (c *genericClient) Type() types.Type {
	return c.netType
}

// isPrivateIP checks if an IP address is in a private range
// (RFC1918 for IPv4, RFC4193 for IPv6, plus loopback and link-local)
func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	// Check for loopback
	if ip.IsLoopback() {
		return true
	}
	// Check for link-local
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	// IPv4 private ranges (RFC1918)
	// 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
	if ip4 := ip.To4(); ip4 != nil {
		return ip4[0] == 10 ||
			(ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31) ||
			(ip4[0] == 192 && ip4[1] == 168) ||
			(ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127) // CGNAT RFC6598
	}
	// IPv6 private range (fc00::/7)
	if len(ip) == net.IPv6len {
		return ip[0] == 0xfc || ip[0] == 0xfd
	}
	return false
}
