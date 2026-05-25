// Package network client.go
package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/transport/network/handshake"
	"github.com/skycoin/skywire/pkg/transport/network/porter"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

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
	ARClient   addrresolver.APIClient
	EB         *appevent.Broadcaster
	DmsgC      *dmsg.Client
	MLogger    *logging.MasterLogger
	// OnExternalSTCPR is called when an incoming STCPR connection is detected
	// from an external (non-LAN) IP address. This validates that the visor is
	// reachable from the internet.
	OnExternalSTCPR func()
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

	resolved := &resolvedClient{genericClient: generic, ar: f.ARClient}

	switch netType {
	case types.STCP:
		return newStcp(generic, f.PKTable), nil
	case types.STCPR:
		return newStcpr(resolved, port), nil
	case types.SUDPH:
		return newSudph(resolved, port), nil
	case types.DMSG:
		return newDmsgClient(f.DmsgC), nil
	}
	return nil, fmt.Errorf("cannot initiate client, type %s not supported", netType)
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
	eb     *appevent.Broadcaster

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
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true) //nolint:errcheck
	}
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

// resolvedClient is a wrapper around genericClient,
// for the types of transports that use address resolver service
// to resolve addresses of remote visors
type resolvedClient struct {
	*genericClient
	ar addrresolver.APIClient
}

type dialFunc func(ctx context.Context, addr string) (net.Conn, error)

// dialVisor uses address resovler to obtain network address of the target visor
// and dials that visor address(es)
// dial process is specific to transport type and is provided by the client
func (c *resolvedClient) dialVisor(ctx context.Context, rPK cipher.PubKey, dial dialFunc) (net.Conn, error) {
	visorData, err := c.ar.Resolve(ctx, string(c.netType), rPK)
	if err != nil {
		return nil, fmt.Errorf("resolve PK: %w", err)
	}
	c.log.Debugf("Resolved PK %v to visor data %v", rPK, visorData)

	// For self-connections (rPK == local PK), always try local addresses first
	// to avoid NAT hairpinning issues when connecting via public IP
	isSelfConnection := rPK == c.lPK

	// Check if the remote visor shares our public IP (same NAT)
	// In this case, we should try LAN addresses first to avoid NAT hairpinning issues
	samePublicIP := false
	localPublicIP := c.ar.LocalPublicIP()
	if localPublicIP != "" && visorData.RemoteAddr != "" {
		remoteIP := visorData.RemoteAddr
		// Extract just the IP if RemoteAddr includes a port
		if host, _, err := net.SplitHostPort(remoteIP); err == nil {
			remoteIP = host
		}
		samePublicIP = localPublicIP == remoteIP
		if samePublicIP {
			c.log.Debugf("Remote visor shares same public IP (%s), trying LAN addresses first", localPublicIP)
		}
	}

	if visorData.IsLocal || isSelfConnection || samePublicIP {
		if isSelfConnection {
			c.log.Debug("Detected self-connection, trying local addresses to avoid NAT issues")
		}
		// Get the public IP to filter it out from LAN addresses
		remotePublicIP := visorData.RemoteAddr
		if host, _, err := net.SplitHostPort(remotePublicIP); err == nil {
			remotePublicIP = host
		}
		for _, host := range visorData.Addresses {
			// Skip loopback addresses unless it's a self-connection
			if !isSelfConnection && (host == "127.0.0.1" || host == "::1") {
				continue
			}
			// Skip the public IP - we'll fall back to it anyway
			if host == remotePublicIP {
				continue
			}
			addr := net.JoinHostPort(host, visorData.Port)
			c.log.Debugf("Trying LAN address: %s", addr)
			conn, err := dial(ctx, addr)
			if err == nil {
				c.log.Debugf("Successfully connected via LAN address: %s", addr)
				return conn, nil
			}
			c.log.WithError(err).Debugf("Failed to dial %s, trying next address", addr)
		}
	}

	// Happy Eyeballs (#1525 Phase 3): when AR has both a v6 and v4
	// public address for the peer, try v6 first; fall back to v4 on
	// any v6 failure (refused, timeout, unreachable). Sequential —
	// no parallel race, no double-noise-handshake risk. The v6 attempt
	// gets a bounded window (v6HeadStart) so a black-holed v6 route
	// doesn't extend the overall dial materially beyond the v4 path's
	// own connect time.
	//
	// Backward-compat: v6-only and v4-only peers are dispatched
	// directly (no head-start cost). The pre-#1525 single-stack
	// behavior — "RemoteAddrV6 is empty" — falls through the
	// addrV6 == "" branch and dials RemoteAddr exactly like before.
	addrV4 := canonicalAddr(visorData.RemoteAddr, visorData.Port)
	addrV6 := canonicalAddr(visorData.RemoteAddrV6, visorData.Port)
	switch {
	case addrV6 != "" && addrV4 != "":
		c.log.Debugf("Happy Eyeballs: dual-stack %s (v6) / %s (v4)", addrV6, addrV4)
		return happyEyeballsDial(ctx, addrV6, addrV4, dial, c.log)
	case addrV6 != "":
		c.log.Debugf("Dialing v6-only public address: %s", addrV6)
		return dial(ctx, addrV6)
	case addrV4 != "":
		c.log.Debugf("Dialing v4-only public address: %s", addrV4)
		return dial(ctx, addrV4)
	default:
		return nil, fmt.Errorf("visor data has neither RemoteAddr nor RemoteAddrV6")
	}
}

// v6HeadStart bounds the v6 attempt in happyEyeballsDial. Modeled
// on RFC 8305's 250ms head-start, widened to 1s to give a slow but
// reachable v6 route a fair chance before falling back to v4. A
// black-holed v6 route still bounds the extra latency to this much.
const v6HeadStart = time.Second

// canonicalAddr returns "" when raw is empty (lets the caller branch
// on "v6 unavailable"), otherwise appends port when raw is bare-host.
// Mirrors the inline check the pre-#1525 dialer did so the v4/v6
// branches stay symmetric.
func canonicalAddr(raw, port string) string {
	if raw == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(raw); err != nil {
		return net.JoinHostPort(raw, port)
	}
	return raw
}

// happyEyeballsDial tries v6 first with a v6HeadStart-bounded sub-ctx,
// falls back to v4 on any v6 failure. Sequential by design: one
// socket + one noise handshake in flight at a time. Caller-supplied
// ctx still bounds the overall dial; the v6 sub-ctx only narrows the
// v6 attempt's deadline within ctx's window.
func happyEyeballsDial(ctx context.Context, addrV6, addrV4 string, dial dialFunc, log *logging.Logger) (net.Conn, error) {
	v6Ctx, cancel := context.WithTimeout(ctx, v6HeadStart)
	conn, v6Err := dial(v6Ctx, addrV6)
	cancel()
	if v6Err == nil {
		log.Debugf("Happy Eyeballs: v6 connected: %s", addrV6)
		return conn, nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("happy eyeballs: ctx done during v6 attempt: %w", ctx.Err())
	}
	log.WithError(v6Err).Debugf("Happy Eyeballs: v6 %s failed, falling back to v4 %s", addrV6, addrV4)
	conn, v4Err := dial(ctx, addrV4)
	if v4Err != nil {
		return nil, fmt.Errorf("happy eyeballs: v6 (%s) failed: %v; v4 (%s) failed: %w", addrV6, v6Err, addrV4, v4Err)
	}
	log.Debugf("Happy Eyeballs: v4 fallback connected: %s", addrV4)
	return conn, nil
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
