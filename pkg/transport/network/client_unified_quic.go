//go:build !tinygo

// Package network pkg/transport/network/client_unified_quic.go c2-net-transport
//
// sharedQUICMux multiplexes several QUIC-based application protocols onto ONE
// quic.Transport over the unified transport_port UDP socket, so a visor accepts
// skywire-QUIC ("squicr", ALPN skywire-quic-1) AND WebTransport ("swtr", ALPN
// "h3") on a SINGLE forwardable UDP port instead of one socket each. Without
// this, WebTransport bound its own ephemeral UDP port — reachable on a bare
// public IP but NOT through a single port-forward (the operator forwards
// transport_port, not WT's random port), so a NAT'd-but-forwarded public visor
// was unreachable over WT (the carrier a browser visor depends on).
//
// quic-go permits only one listener per packet conn, so we run ONE ListenEarly
// whose tls.Config.GetConfigForClient picks the per-ALPN server config (its own
// cert + client-auth policy) from the ClientHello's offered ALPNs, and ONE
// accept loop that hands each connection to the handler registered for its
// negotiated ALPN. squicr needs mutual TLS (RequireAnyClientCert + a PK-bound
// cert); WT needs none (a browser can't present a client cert) — incompatible
// in a fixed tls.Config, but fine when GetConfigForClient returns a different
// config per ALPN. Inbound WT (QUIC) and squicr (QUIC) packets are
// indistinguishable at the udpDemux layer (both classify as protoQUIC); the
// ALPN split here is what lets them coexist on that one demux conn.
package network

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"

	"github.com/quic-go/quic-go"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyquic"
)

// ALPNs multiplexed on the shared QUIC socket.
const (
	quicALPN = skyquic.NextProto             // "skywire-quic-1" — squicr
	wtALPN   = skyquic.WebTransportNextProto // "h3"             — WebTransport
)

// sharedQUICMux owns one quic.Transport on the unified UDP socket's QUIC demux
// conn and dispatches accepted connections to a per-ALPN handler.
type sharedQUICMux struct {
	tr   *quic.Transport
	conf *quic.Config
	log  *logging.Logger

	mu      sync.Mutex
	handler map[string]func(*quic.Conn) // negotiated ALPN -> handler
	tlsByAL map[string]*tls.Config      // offered ALPN     -> server tls.Config
	lis     *quic.EarlyListener
	closed  bool
}

// newSharedQUICMux prepares a multiplexer over conn (the udpDemux protoQUIC
// virtual conn = the master transport_port socket). The single quic.Config
// enables BOTH datagrams and stream-reset-partial-delivery: webtransport-go
// requires both, and they are harmless for squicr (negotiated per-connection).
func newSharedQUICMux(conn net.PacketConn, log *logging.Logger) *sharedQUICMux {
	return &sharedQUICMux{
		tr: &quic.Transport{Conn: conn},
		conf: &quic.Config{
			EnableDatagrams:                  true,
			EnableStreamResetPartialDelivery: true,
			MaxIdleTimeout:                   quicMaxIdleTimeout,
			KeepAlivePeriod:                  quicKeepAlivePeriod,
		},
		log:     log,
		handler: make(map[string]func(*quic.Conn)),
		tlsByAL: make(map[string]*tls.Config),
	}
}

// register wires an ALPN's server tls.Config + connection handler, starting the
// single shared listener on the first registration. Each QUIC-based serving
// client (squicr, WT) calls this from its serve path; order doesn't matter — a
// connection offering an ALPN whose client hasn't registered yet is rejected by
// getConfigForClient and the dialer retries.
func (m *sharedQUICMux) register(alpn string, tlsConf *tls.Config, handle func(*quic.Conn)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return net.ErrClosed
	}
	m.tlsByAL[alpn] = tlsConf
	m.handler[alpn] = handle
	if m.lis == nil {
		return m.startLocked()
	}
	return nil
}

func (m *sharedQUICMux) startLocked() error {
	base := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		GetConfigForClient: m.getConfigForClient,
	}
	lis, err := m.tr.ListenEarly(base, m.conf)
	if err != nil {
		return fmt.Errorf("sharedQUIC: listen: %w", err)
	}
	m.lis = lis
	go m.acceptLoop()
	return nil
}

// getConfigForClient returns the server config for the first offered ALPN that
// has a registered handler, narrowed to that single ALPN so TLS negotiates it.
// crypto/tls (via quic-go's tls.QUICServer) invokes this with the ClientHello,
// whose SupportedProtos carries the offered ALPNs.
func (m *sharedQUICMux) getConfigForClient(hello *tls.ClientHelloInfo) (*tls.Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, proto := range hello.SupportedProtos {
		if tc, ok := m.tlsByAL[proto]; ok {
			cfg := tc.Clone()
			cfg.NextProtos = []string{proto}
			return cfg, nil
		}
	}
	return nil, fmt.Errorf("sharedQUIC: no registered handler for offered ALPNs %v", hello.SupportedProtos)
}

func (m *sharedQUICMux) acceptLoop() {
	for {
		conn, err := m.lis.Accept(context.Background())
		if err != nil {
			return // listener closed
		}
		alpn := conn.ConnectionState().TLS.NegotiatedProtocol
		m.mu.Lock()
		handle := m.handler[alpn]
		m.mu.Unlock()
		if handle == nil {
			// Unreachable in practice: getConfigForClient only completes a
			// handshake for a registered ALPN.
			conn.CloseWithError(quicErrNoStream, "no handler for negotiated alpn") //nolint:errcheck,gosec
			continue
		}
		go handle(conn)
	}
}

// localAddr is the master socket's address (= transport_port); clients use it to
// advertise/register their shared-socket endpoint with the address resolver.
func (m *sharedQUICMux) localAddr() net.Addr { return m.tr.Conn.LocalAddr() }

// Close stops the shared listener and transport. The underlying packet conn
// (the demux's master socket) is owned by CloseUnifiedUDP, not here.
func (m *sharedQUICMux) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	var err error
	if m.lis != nil {
		err = m.lis.Close()
	}
	_ = m.tr.Close() //nolint:errcheck
	return err
}

// chanListener is a net.Listener whose connections are pushed in by a
// sharedQUICMux ALPN handler (rather than pulled from a socket), so each
// registered QUIC protocol exposes a standard net.Listener that
// genericClient.acceptTransports consumes unchanged.
type chanListener struct {
	addr  net.Addr
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newChanListener(addr net.Addr) *chanListener {
	return &chanListener{addr: addr, conns: make(chan net.Conn, 16), done: make(chan struct{})}
}

// push hands an accepted/adapted conn to Accept, dropping it if the listener is
// closed.
func (l *chanListener) push(c net.Conn) {
	select {
	case l.conns <- c:
	case <-l.done:
		c.Close() //nolint:errcheck,gosec
	}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *chanListener) Addr() net.Addr { return l.addr }
