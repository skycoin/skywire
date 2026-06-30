//go:build !tinygo

// Package network pkg/transport/network/quic.go: a QUIC-based skywire
// transport. Mirrors the sudph/stcpr structure (AR-resolved, UDP-based)
// but rides quic-go instead of KCP: the connection is secured by the
// skywire-PK-bound TLS identity (quic_identity.go, option A), one QUIC
// stream carries one skywire transport, and the existing handshake +
// noise layers run over the stream exactly as for the other types.
//
// QUIC gives reliable streams AND RFC 9221 unreliable datagrams over one
// connection — the stream half is wired here; the datagram half (which
// makes faithful-UDP wire-real) is the follow-on tp.WriteDatagram path.
package network

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	quicReRegisterInterval  = 90 * time.Second
	quicBindRetryDelay      = 5 * time.Second
	quicBindMaxRetryDelay   = 60 * time.Second
	quicMaxIdleTimeout      = 60 * time.Second
	quicKeepAlivePeriod     = 25 * time.Second
	quicStreamAcceptTimeout = 30 * time.Second
)

// quicErrNoStream is the QUIC application error code used when closing a
// connection that never opened (dial side) or yielded (accept side) a stream.
const quicErrNoStream quic.ApplicationErrorCode = 1

type quicClient struct {
	*resolvedClient
	// table statically pins peer PK → UDP address ("host:port"), the QUIC analog
	// of stcp's pk_table (transport.quic_table). Dial consults it before the
	// address resolver, so a configured peer dials with no AR lookup — exactly
	// like the WS/WT clients. nil/empty = AR-only (the default).
	table stcp.PKTable
	port  int
	// sharedConn, when set, is the UDP socket QUIC listens over instead of
	// binding its own — the QUIC demux conn of a unified transport_port socket
	// (see udpDemux). nil = bind a dedicated socket (the default / per-type-port
	// behavior). The master socket's lifecycle is owned by the demux, not here.
	sharedConn net.PacketConn
	// mux, when set (unified transport_port + ephemeral quic_port), is the shared
	// QUIC multiplexer squicr registers its skywire-quic-1 ALPN handler on, so it
	// coexists with WebTransport on ONE quic.Transport over the master socket.
	mux      *sharedQUICMux
	udpConn  net.PacketConn
	listener *quic.Listener
	tlsCert  tls.Certificate
	qconf    *quic.Config
}

// makeQuicClient is the build-tagged QUIC constructor used by MakeClient. On
// non-TinyGo it builds a real quicClient; the TinyGo build (quic_tinygo.go)
// returns an unsupported error so the browser graph never pulls quic-go.
func makeQuicClient(resolved *resolvedClient, port int) (Client, error) { //nolint:unparam // error is for build-tag parity with the tinygo stub
	return newQuic(resolved, port), nil
}

func newQuic(resolved *resolvedClient, port int) Client {
	client := &quicClient{resolvedClient: resolved, port: port}
	client.netType = types.QUIC
	return client
}

// tableAddr looks up a statically-configured UDP address for rPK. The table is
// a nil interface when unconfigured (AR-only), so guard it — calling a method on
// a nil interface would panic the visor (same guard as the WS client).
func (c *quicClient) tableAddr(rPK cipher.PubKey) (string, bool) {
	if c.table == nil {
		return "", false
	}
	return c.table.Addr(rPK)
}

// quicStreamConn adapts a single QUIC stream (+ its connection) to net.Conn.
// quic.Stream provides Read/Write/Close/deadlines but not Local/RemoteAddr;
// those come from the connection. Close tears down the whole QUIC connection
// (and the per-dial UDP socket on the dial side) — one transport == one
// connection in this v1 (stream multiplexing is a later optimization).
type quicStreamConn struct {
	*quic.Stream
	conn    *quic.Conn
	udpConn net.PacketConn // non-nil on the dial side (per-dial ephemeral socket)
}

func (c *quicStreamConn) LocalAddr() net.Addr  { return c.conn.LocalAddr() }
func (c *quicStreamConn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }

// WriteDatagram / ReadDatagram expose the QUIC connection's RFC 9221
// unreliable datagram channel, making *quicStreamConn a DatagramConn. The
// router sends faithful-UDP DatagramPackets over this channel (no head-of-line
// blocking, genuine loss tolerance) instead of the reliable stream. These run
// on the raw QUIC connection — below the noise stream wrapper — so the
// transport layer captures the capability before wrapping (see transport.encrypt).
func (c *quicStreamConn) WriteDatagram(b []byte) error {
	return c.conn.SendDatagram(b)
}

func (c *quicStreamConn) ReadDatagram(ctx context.Context) ([]byte, error) {
	return c.conn.ReceiveDatagram(ctx)
}

// compile-time assertion that *quicStreamConn provides the native datagram channel.
var _ DatagramConn = (*quicStreamConn)(nil)

func (c *quicStreamConn) Close() error {
	_ = c.Stream.Close() //nolint:errcheck,gosec // half-close the stream, then the conn
	err := c.conn.CloseWithError(0, "closed")
	if c.udpConn != nil {
		c.udpConn.Close() //nolint:errcheck,gosec
	}
	return err
}

// quicListener adapts a *quic.Listener to net.Listener. Each accepted QUIC
// connection yields one bidirectional stream = one skywire transport. Stream
// acceptance runs per-connection in a goroutine so a peer that connects but
// never opens a stream can't stall the accept loop.
type quicListener struct {
	lis   *quic.Listener
	conns chan net.Conn
	done  chan struct{}
	log   *logging.Logger
}

func newQuicListener(lis *quic.Listener, log *logging.Logger) *quicListener {
	l := &quicListener{lis: lis, conns: make(chan net.Conn, 16), done: make(chan struct{}), log: log}
	go l.acceptLoop()
	return l
}

func (l *quicListener) acceptLoop() {
	for {
		conn, err := l.lis.Accept(context.Background())
		if err != nil {
			close(l.conns)
			return
		}
		go l.handleConn(conn)
	}
}

func (l *quicListener) handleConn(conn *quic.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), quicStreamAcceptTimeout)
	defer cancel()
	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		conn.CloseWithError(quicErrNoStream, "no stream opened") //nolint:errcheck,gosec
		return
	}
	select {
	case l.conns <- &quicStreamConn{Stream: stream, conn: conn}:
	case <-l.done:
		conn.CloseWithError(0, "listener closed") //nolint:errcheck,gosec
	}
}

func (l *quicListener) Accept() (net.Conn, error) {
	c, ok := <-l.conns
	if !ok {
		return nil, net.ErrClosed
	}
	return c, nil
}

func (l *quicListener) Close() error {
	close(l.done)
	return l.lis.Close()
}

func (l *quicListener) Addr() net.Addr { return l.lis.Addr() }

// Dial implements Client.
func (c *quicClient) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (Transport, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}
	c.log.Debugf("Dialing PK %v over QUIC", rPK)
	// Endpoint resolution order: static PK table first (a manually-configured
	// peer — the QUIC analog of stcp's pk_table), then the address resolver.
	// Mirrors the WS/WT clients; a nil/empty table just falls through to the AR.
	var conn net.Conn
	var err error
	if addr, ok := c.tableAddr(rPK); ok {
		c.log.Debugf("Resolved PK %v to %s via static QUIC table", rPK, addr)
		conn, err = c.dial(ctx, addr, rPK)
	} else {
		conn, err = c.dialVisor(ctx, rPK, func(ctx context.Context, addr string) (net.Conn, error) {
			return c.dial(ctx, addr, rPK)
		})
	}
	if err != nil {
		return nil, err
	}
	tp, err := c.initTransport(ctx, conn, rPK, rPort)
	if err != nil {
		conn.Close() //nolint:errcheck,gosec
		return nil, err
	}
	return tp, nil
}

func (c *quicClient) dial(ctx context.Context, addr string, rPK cipher.PubKey) (net.Conn, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("resolve udp addr %q: %w", addr, err)
	}
	// Dial from a fresh ephemeral UDP socket. (Reusing the listen socket
	// for both roles needs quic.Transport routing — a later optimization;
	// ephemeral dial sockets work for publicly-reachable peers, which is the
	// v1 target. NAT hole-punching is a follow-up.)
	dialUDP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("bind quic dial socket: %w", err)
	}
	tlsConf := quicTLSConfig(c.tlsCert, &rPK, nil) // pin the remote PK at the TLS layer
	qc, err := quic.Dial(ctx, dialUDP, udpAddr, tlsConf, c.qconf)
	if err != nil {
		dialUDP.Close() //nolint:errcheck,gosec
		return nil, err
	}
	stream, err := qc.OpenStreamSync(ctx)
	if err != nil {
		qc.CloseWithError(quicErrNoStream, "open stream") //nolint:errcheck,gosec
		dialUDP.Close()                                   //nolint:errcheck,gosec
		return nil, err
	}
	return &quicStreamConn{Stream: stream, conn: qc, udpConn: dialUDP}, nil
}

// Start implements Client.
func (c *quicClient) Start() error {
	if c.connListener != nil {
		return ErrAlreadyListening
	}
	cert, err := newQUICCertificate(c.PK(), c.SK())
	if err != nil {
		return fmt.Errorf("quic: build identity certificate: %w", err)
	}
	c.tlsCert = cert
	c.qconf = &quic.Config{
		EnableDatagrams: true,
		MaxIdleTimeout:  quicMaxIdleTimeout,
		KeepAlivePeriod: quicKeepAlivePeriod,
	}
	go c.serve()
	return nil
}

func (c *quicClient) serve() {
	lis, err := c.listen()
	if err != nil {
		c.log.Errorf("Failed to listen: %v", err)
		return
	}
	c.acceptTransports(lis)
}

// listenShared registers squicr's skywire-quic-1 ALPN handler on the shared QUIC
// multiplexer (which owns the single quic.Transport on the master socket) and
// returns a chan-backed listener fed by accepted squicr connections. WT shares
// the same socket via its own "h3" ALPN registration. The advertised/registered
// AR port is the master socket's port (= transport_port).
func (c *quicClient) listenShared() (net.Listener, error) {
	addr := c.sharedConn.LocalAddr()
	// Leave c.udpConn nil: the master socket + quic.Transport are owned by the
	// shared mux (closed via CloseUnifiedUDP), not by this client's Close.
	lis := newChanListener(addr)
	srvTLS := quicTLSConfig(c.tlsCert, nil, nil) // accept any valid skywire PK; ALPN skywire-quic-1
	handle := func(conn *quic.Conn) {
		ctx, cancel := context.WithTimeout(context.Background(), quicStreamAcceptTimeout)
		defer cancel()
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			conn.CloseWithError(quicErrNoStream, "no stream opened") //nolint:errcheck,gosec
			return
		}
		lis.push(&quicStreamConn{Stream: stream, conn: conn})
	}
	if err := c.mux.register(quicALPN, srvTLS, handle); err != nil {
		return nil, fmt.Errorf("quic: register on shared mux: %w", err)
	}
	_, localPort, err := net.SplitHostPort(addr.String())
	if err != nil {
		return nil, err
	}
	c.log.Debugf("squicr serving on shared transport_port socket %s", localPort)
	go c.bindAndReRegister(localPort)
	return lis, nil
}

func (c *quicClient) listen() (net.Listener, error) {
	if c.sharedConn != nil && c.mux != nil {
		return c.listenShared()
	}
	var udpConn net.PacketConn
	if c.sharedConn != nil {
		// Unified transport port: listen over the shared socket's QUIC demux conn.
		// quic.Listen does not own the PacketConn, so the master socket lifecycle
		// stays with the demux.
		udpConn = c.sharedConn
	} else {
		var err error
		confPort := ""
		if c.port != 0 {
			confPort = fmt.Sprintf(":%d", c.port)
		}
		for {
			udpConn, err = net.ListenPacket("udp", confPort)
			if err != nil {
				c.log.WithError(err).Warnf("Failed to listen on UDP port %d", c.port)
				c.port++
				confPort = fmt.Sprintf(":%d", c.port)
				continue
			}
			break
		}
	}
	c.udpConn = udpConn

	srvTLS := quicTLSConfig(c.tlsCert, nil, nil) // accept any valid skywire PK
	qlis, err := quic.Listen(udpConn, srvTLS, c.qconf)
	if err != nil {
		udpConn.Close() //nolint:errcheck,gosec
		return nil, err
	}
	c.listener = qlis

	_, localPort, err := net.SplitHostPort(udpConn.LocalAddr().String())
	if err != nil {
		return nil, err
	}
	c.log.Debugf("Successfully bound quic to UDP port %s", localPort)

	// Register the QUIC UDP port with the address resolver (best-effort,
	// retrying — mirrors STCPR). Without AR, dialing-by-PK can't resolve us,
	// but we still listen so direct-address dials / tests work.
	go c.bindAndReRegister(localPort)

	return newQuicListener(qlis, c.log), nil
}

func (c *quicClient) bindAndReRegister(port string) {
	delay := quicBindRetryDelay
	for attempt := 1; ; attempt++ {
		if err := c.ar.BindQUIC(context.Background(), port); err == nil {
			c.log.Debugf("Successfully bound QUIC to port %s on address resolver", port)
			break
		} else {
			c.log.WithError(err).Warnf("Failed to bind QUIC (attempt %d), retrying in %v", attempt, delay)
		}
		if c.isClosed() {
			return
		}
		time.Sleep(delay)
		delay *= 2
		if delay > quicBindMaxRetryDelay {
			delay = quicBindMaxRetryDelay
		}
	}

	ticker := time.NewTicker(quicReRegisterInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			c.log.Debug("Stopping QUIC re-registration loop")
			return
		case <-ticker.C:
			if err := c.ar.BindQUIC(context.Background(), port); err != nil {
				c.log.WithError(err).Warn("Failed to re-register QUIC with address resolver")
			}
		}
	}
}

// Close implements Client. Overrides genericClient.Close to also close the
// underlying UDP socket (quic.Listen does not own the PacketConn).
func (c *quicClient) Close() error {
	err := c.resolvedClient.Close()
	if c.udpConn != nil {
		c.udpConn.Close() //nolint:errcheck,gosec
	}
	return err
}
