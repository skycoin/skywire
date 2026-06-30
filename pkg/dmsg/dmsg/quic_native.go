//go:build !tinygo

// Package dmsg pkg/dmsg/quic_native.go
//
// All quic-go-dependent code for dmsg-over-QUIC (#2607) lives here, behind the
// //go:build !tinygo tag, because quic-go does not compile under TinyGo. The
// session core (session_common.go / stream.go / server_session.go / …) refers
// to QUIC only through the quicConn / quicStream interfaces (quic_iface.go); the
// concrete *quic.Conn / *quic.Stream adapters, the QUIC session constructors,
// and the client dial + server listen paths are all here. See
// docs/design/tinygo-dmsg-client.md.
package dmsg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg/metrics"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/skyquic"
)

// --- quic-go → quicConn/quicStream adapters ---

// quicConnWrap adapts *quic.Conn to the quicConn interface. SendDatagram and
// ReceiveDatagram are promoted from the embedded *quic.Conn; OpenStreamSync /
// AcceptStream / CloseWithError are shadowed to drop the quic-go types from
// their signatures.
type quicConnWrap struct{ *quic.Conn }

func (c quicConnWrap) OpenStreamSync(ctx context.Context) (quicStream, error) {
	s, err := c.Conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	return quicStreamWrap{s}, nil
}

func (c quicConnWrap) AcceptStream(ctx context.Context) (quicStream, error) {
	s, err := c.Conn.AcceptStream(ctx)
	if err != nil {
		return nil, err
	}
	return quicStreamWrap{s}, nil
}

func (c quicConnWrap) CloseWithError(code uint64, msg string) error {
	return c.Conn.CloseWithError(quic.ApplicationErrorCode(code), msg)
}

// quicStreamWrap adapts *quic.Stream to the quicStream interface. Read / Write /
// Close / SetReadDeadline / SetWriteDeadline (muxStreamConn) are promoted from
// the embedded *quic.Stream.
type quicStreamWrap struct{ *quic.Stream }

func (s quicStreamWrap) quicStreamID() uint32 { return uint32(s.Stream.StreamID()) } //nolint:gosec // id for logging only

// quicAddrConn adapts a *quic.Conn to net.Conn for the addr-only accessors
// (LocalTCPAddr/RemoteTCPAddr/SrcConn) on a QUIC session. The session never
// reads/writes it directly — QUIC data flows over QUIC streams — so Read/Write
// are inert stubs.
type quicAddrConn struct{ qc *quic.Conn }

func (c quicAddrConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c quicAddrConn) Write([]byte) (int, error)        { return 0, io.ErrClosedPipe }
func (c quicAddrConn) Close() error                     { return c.qc.CloseWithError(0, "") }
func (c quicAddrConn) LocalAddr() net.Addr              { return c.qc.LocalAddr() }
func (c quicAddrConn) RemoteAddr() net.Addr             { return c.qc.RemoteAddr() }
func (c quicAddrConn) SetDeadline(time.Time) error      { return nil }
func (c quicAddrConn) SetReadDeadline(time.Time) error  { return nil }
func (c quicAddrConn) SetWriteDeadline(time.Time) error { return nil }

// initQUIC sets up a session (client or server) over a QUIC connection — no
// Noise handshake. The PK-bound QUIC TLS (pkg/skyquic, option A) already
// authenticated the peer's skywire PK and encrypts the client↔server hop;
// dmsg streams ARE QUIC streams. rPK is the peer PK: the dialer knows it up
// front; the server learns it from the verified QUIC TLS certificate.
func (sc *SessionCommon) initQUIC(entity *EntityCommon, qc *quic.Conn, rPK cipher.PubKey) {
	sc.entity = entity
	sc.rPK = rPK
	sc.netConn = quicAddrConn{qc: qc}
	sc.sm.quic = quicConnWrap{qc}
	sc.sm.addr = qc.RemoteAddr()
	sc.ns = nil
	sc.nw = nil
	sc.log = entity.log.WithField("session", rPK)
}

// makeClientSessionQUIC builds a client session over an already-handshaked QUIC
// connection (#2607). No Noise handshake — initQUIC just records the conn; the
// QUIC TLS already authenticated rPK and encrypts the hop.
func makeClientSessionQUIC(entity *EntityCommon, porter *netutil.Porter, qc *quic.Conn, rPK cipher.PubKey) ClientSession {
	var cSes ClientSession
	cSes.SessionCommon = new(SessionCommon)
	cSes.SessionCommon.initQUIC(entity, qc, rPK)
	cSes.porter = porter
	return cSes
}

// makeServerSessionQUIC builds a server session over an already-handshaked QUIC
// connection (#2607). No Noise handshake — the QUIC TLS authenticated rPK
// (learned from the peer's certificate) and encrypts the hop.
func makeServerSessionQUIC(m metrics.Metrics, entity *EntityCommon, qc *quic.Conn, rPK cipher.PubKey) *ServerSession {
	var sSes ServerSession
	sSes.SessionCommon = new(SessionCommon)
	sSes.SessionCommon.initQUIC(entity, qc, rPK)
	sSes.m = m
	return &sSes
}

// dialSessionQUIC dials a dmsg server's QUIC endpoint (Server.AddressUDP). The
// PK-bound QUIC TLS (pkg/skyquic, option A) authenticates the server's skywire
// PK and encrypts the hop; the session runs over native QUIC streams with no
// Noise handshake. EnableDatagrams so the session can later expose an unreliable
// datagram channel.
func (ce *Client) dialSessionQUIC(ctx context.Context, entry *disc.Entry) (ClientSession, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", entry.Server.AddressUDP)
	if err != nil {
		return ClientSession{}, fmt.Errorf("quic: resolve %q: %w", entry.Server.AddressUDP, err)
	}
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return ClientSession{}, fmt.Errorf("quic: bind dial socket: %w", err)
	}
	cert, err := skyquic.NewCertificate(ce.pk, ce.sk)
	if err != nil {
		udpConn.Close() //nolint:errcheck,gosec
		return ClientSession{}, fmt.Errorf("quic: identity cert: %w", err)
	}
	rPK := entry.Static
	tlsConf := skyquic.TLSConfig(cert, &rPK, nil) // pin the server PK
	qc, err := quic.Dial(ctx, udpConn, udpAddr, tlsConf, &quic.Config{
		EnableDatagrams: true,
		KeepAlivePeriod: 25 * time.Second,
		MaxIdleTimeout:  60 * time.Second,
	})
	if err != nil {
		udpConn.Close() //nolint:errcheck,gosec
		return ClientSession{}, fmt.Errorf("quic: dial %s: %w", entry.Server.AddressUDP, err)
	}
	return makeClientSessionQUIC(&ce.EntityCommon, ce.porter, qc, rPK), nil
}

// ServeQUIC starts a QUIC listener for dmsg-over-QUIC (#2607) on the given UDP
// socket and advertises advertisedUDPAddr in discovery (Server.AddressUDP +
// Protocol "quic"). It runs alongside the TCP Serve: QUIC-capable clients dial
// QUIC for native stream multiplexing + a datagram channel, everyone else stays
// on TCP. Blocks until the listener errors or the server closes.
func (s *Server) ServeQUIC(udpConn net.PacketConn, advertisedUDPAddr string) error {
	cert, err := skyquic.NewCertificate(s.pk, s.sk)
	if err != nil {
		return fmt.Errorf("dmsg-quic: identity cert: %w", err)
	}
	// Server-side: accept any valid skywire PK; the peer PK is learned from
	// the verified client certificate (see quicPeerPK).
	tlsConf := skyquic.TLSConfig(cert, nil, nil)
	lis, err := quic.Listen(udpConn, tlsConf, &quic.Config{
		EnableDatagrams: true,
		MaxIdleTimeout:  60 * time.Second,
		KeepAlivePeriod: 25 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("dmsg-quic: listen: %w", err)
	}
	s.setAdvertisedUDPAddr(advertisedUDPAddr)
	s.log.WithField("addr_udp", advertisedUDPAddr).Info("Serving dmsg over QUIC.")
	for {
		qc, err := lis.Accept(context.Background())
		if err != nil {
			if isClosed(s.done) {
				return nil
			}
			return fmt.Errorf("dmsg-quic: accept: %w", err)
		}
		go s.handleQUICConn(qc)
	}
}

// quicPeerPK returns the skywire PK the connecting client authenticated with via
// its PK-bound QUIC TLS certificate (pkg/skyquic, option A).
func quicPeerPK(qc *quic.Conn) (cipher.PubKey, error) {
	certs := qc.ConnectionState().TLS.PeerCertificates
	if len(certs) == 0 {
		return cipher.PubKey{}, errors.New("dmsg-quic: peer presented no certificate")
	}
	raw := make([][]byte, len(certs))
	for i, c := range certs {
		raw[i] = c.Raw
	}
	return skyquic.VerifyCert(raw)
}

// handleQUICConn serves an accepted QUIC connection as a dmsg server session —
// the QUIC-stream analog of handleSession (no Noise; the QUIC TLS already
// authenticated the peer PK + encrypts the hop).
func (s *Server) handleQUICConn(qc *quic.Conn) {
	defer func() {
		if r := recover(); r != nil {
			s.log.WithField("panic", r).WithField("remote_quic", qc.RemoteAddr()).
				Error("Recovered from panic in handleQUICConn, connection will be closed")
			qc.CloseWithError(0, "panic") //nolint:errcheck,gosec
		}
	}()

	log := s.log.WithField("remote_quic", qc.RemoteAddr())
	pk, err := quicPeerPK(qc)
	if err != nil {
		log.WithError(err).Warn("Failed to authenticate QUIC peer")
		qc.CloseWithError(0, "auth failed") //nolint:errcheck,gosec
		return
	}

	dSes := makeServerSessionQUIC(s.m, &s.EntityCommon, qc, pk)
	log = log.WithField("remote_pk", pk)
	if s.isPeerPK(pk) {
		dSes.isPeer = true
		log.Info("Started peer server session (quic).")
	} else {
		log.Info("Started session (quic).")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		awaitDone(ctx, s.done)
		log.WithError(dSes.Close()).Info("Stopped session.")
	}()
	log.Infof("quic stream session initial for %s", pk.String())

	s.setSession(ctx, dSes.SessionCommon)
	dSes.Serve()

	if dSes.isPeer {
		s.peerSessionsMx.Lock()
		if s.peerSessions[pk] == dSes.SessionCommon {
			delete(s.peerSessions, pk)
		}
		s.peerSessionsMx.Unlock()
	}
	s.delSession(ctx, pk, dSes.SessionCommon)
	cancel()
}
