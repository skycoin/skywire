// Package dmsg pkg/dmsg/dmsg/session_common.go c1-net-dmsg
package dmsg

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chen3feng/safecast"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire/third_party/hashicorp/yamux"
	"github.com/xtaci/smux"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
)

// SessionCommon contains the common fields and methods used by a session, whether it be it from the client or server
// perspective.
type SessionCommon struct {
	entity *EntityCommon // back reference
	rPK    cipher.PubKey // remote pk
	isPeer bool          // true if this session is with a peer server

	// carrier records how this session's byte pipe was dialed — one of
	// CarrierTCP / CarrierWS / CarrierWT / CarrierQUIC (empty for accepted
	// server-side sessions). It lets a browser client that bootstrapped over
	// wss converge to WebTransport: see Client.UpgradeBrowserSessions.
	carrier string

	// carrierAddr is the endpoint the carrier actually dialed: host:port for
	// tcp/quic, the ws(s):// URL for ws, the https:// URL for WebTransport.
	// Empty for accepted (server-side) sessions. Together with carrier it
	// tells you the exact protocol a client used to reach its dmsg server
	// (and, for ws, whether it was plain ws:// or secured wss://).
	carrierAddr string

	// wtCapable records whether this session's server advertises a WebTransport
	// endpoint (AddressWT) in discovery. Only WT-capable servers should have their
	// bootstrap wss session dropped by UpgradeBrowserSessions — dropping a wss to a
	// server that can't do WT just re-dials wss on the next tick (churn).
	wtCapable bool

	netConn net.Conn // underlying net.Conn (TCP connection to the dmsg server)
	// ys      *yamux.Session
	// ss      *smux.Session
	sm  SessionManager
	ns  *noise.Noise
	nw  *noise.NonceWindow
	rMx sync.Mutex
	wMx sync.Mutex

	// lastPingNs stores the last measured round-trip latency in nanoseconds.
	// Updated by background ping goroutine; read by DialStream for sorting.
	// A value of 0 means no measurement yet (treated as max latency for sorting).
	lastPingNs atomic.Int64

	log logrus.FieldLogger
}

// SessionManager holds the active multiplexer for a session. Exactly one of
// yamux / smux / quic is non-nil. yamux and smux multiplex streams over a
// single reliable conn (TCP); quic is a QUIC connection whose native streams
// ARE the dmsg streams (no separate mux) and which also carries an unreliable
// datagram channel — #2607 dmsg-over-QUIC.
type SessionManager struct {
	mutx  sync.RWMutex
	yamux *yamux.Session
	smux  *smux.Session
	quic  quicConn
	addr  net.Addr
}

// NumStreams reports the number of open mux streams on this session. Returns -1
// when the count is unavailable — a quic session's native streams aren't cheaply
// countable, and a not-yet-established manager has no mux — which the idle-session
// reaper treats as "busy" so it never reaps a session it can't measure.
func (sm *SessionManager) NumStreams() int {
	sm.mutx.RLock()
	defer sm.mutx.RUnlock()
	switch {
	case sm.yamux != nil:
		return sm.yamux.NumStreams()
	case sm.smux != nil:
		return sm.smux.NumStreams()
	default:
		return -1
	}
}

// NumStreams reports the number of open mux streams on this session (0 = idle;
// -1 = unmeasurable, treated as busy). Used by the idle-session reaper.
func (sc *SessionCommon) NumStreams() int { return sc.sm.NumStreams() }

// GetConn returns underlying TCP `net.Conn`.
func (sc *SessionCommon) GetConn() net.Conn {
	return sc.netConn
}

// GetDecNonce returns value of DecNonce of underlying `*noise.Noise`.
func (sc *SessionCommon) GetDecNonce() uint64 {
	sc.rMx.Lock()
	defer sc.rMx.Unlock()
	return sc.ns.GetDecNonce()
}

// GetEncNonce returns value of EncNonce of underlying `*noise.Noise`.
func (sc *SessionCommon) GetEncNonce() uint64 {
	sc.wMx.Lock()
	defer sc.wMx.Unlock()
	return sc.ns.GetEncNonce()
}

func (sc *SessionCommon) initClient(entity *EntityCommon, conn net.Conn, rPK cipher.PubKey) error {
	ns, err := noise.New(noise.HandshakeXK, noise.Config{
		LocalPK:   entity.pk,
		LocalSK:   entity.sk,
		RemotePK:  rPK,
		Initiator: true,
	})
	if err != nil {
		return err
	}

	rw := noise.NewReadWriter(conn, ns)
	if err := rw.Handshake(HandshakeTimeout); err != nil {
		return err
	}
	if rw.Buffered() > 0 {
		return ErrSessionHandshakeExtraBytes
	}
	sc.entity = entity
	sc.rPK = rPK
	sc.netConn = conn
	sc.ns = ns
	sc.nw = noise.NewNonceWindow()
	sc.log = entity.log.WithField("session", ns.RemoteStatic())
	return nil
}

func (sc *SessionCommon) initServer(entity *EntityCommon, conn net.Conn) error {
	ns, err := noise.New(noise.HandshakeXK, noise.Config{
		LocalPK:   entity.pk,
		LocalSK:   entity.sk,
		Initiator: false,
	})
	if err != nil {
		return err
	}

	rw := noise.NewReadWriter(conn, ns)
	if err := rw.Handshake(HandshakeTimeout); err != nil {
		return err
	}
	if rw.Buffered() > 0 {
		return ErrSessionHandshakeExtraBytes
	}

	sc.entity = entity
	sc.rPK = ns.RemoteStatic()
	sc.netConn = conn
	sc.ns = ns
	sc.nw = noise.NewNonceWindow()
	sc.log = entity.log.WithField("session", ns.RemoteStatic())
	return nil
}

// ErrDatagramsUnsupported is returned by the session datagram API for non-QUIC
// sessions (TCP+yamux/smux carries reliable streams only).
var ErrDatagramsUnsupported = errors.New("dmsg: session does not support datagrams (not a QUIC session)")

// SupportsDatagrams reports whether this session can carry unreliable datagrams
// — true only for QUIC sessions (#2607 dmsg-over-QUIC).
func (sc *SessionCommon) SupportsDatagrams() bool {
	sc.sm.mutx.RLock()
	defer sc.sm.mutx.RUnlock()
	return sc.sm.quic != nil
}

// WriteDatagram sends an unreliable datagram over the session's QUIC connection
// (RFC 9221) — UDP-over-dmsg. Returns ErrDatagramsUnsupported on non-QUIC
// sessions. Datagrams ride the same PK-bound-TLS-encrypted QUIC connection as
// the session's streams; this gives dmsg a genuine datagram channel that the
// reliable yamux/smux mux cannot.
func (sc *SessionCommon) WriteDatagram(b []byte) error {
	sc.sm.mutx.RLock()
	qc := sc.sm.quic
	sc.sm.mutx.RUnlock()
	if qc == nil {
		return ErrDatagramsUnsupported
	}
	return qc.SendDatagram(b)
}

// ReadDatagram receives an unreliable datagram from the session's QUIC
// connection. Returns ErrDatagramsUnsupported on non-QUIC sessions.
func (sc *SessionCommon) ReadDatagram(ctx context.Context) ([]byte, error) {
	sc.sm.mutx.RLock()
	qc := sc.sm.quic
	sc.sm.mutx.RUnlock()
	if qc == nil {
		return nil, ErrDatagramsUnsupported
	}
	return qc.ReceiveDatagram(ctx)
}

// initQUIC (the QUIC session setup) lives in quic_native.go (//go:build
// !tinygo) — it constructs the quic-go-typed quicAddrConn / quicConnWrap.

// writeEncryptedGob encrypts with noise and prefixed with uint16 (2 additional bytes).
func (sc *SessionCommon) writeObject(w io.Writer, obj SignedObject) error {
	sc.wMx.Lock()
	// QUIC sessions (sc.ns == nil) skip the per-object Noise encryption: the
	// QUIC stream is already TLS-encrypted (PK-bound), and the object stays
	// signed so dmsg-level authentication is unchanged. Other sessions keep
	// the Noise layer.
	var p []byte
	if sc.ns == nil {
		p = append([]byte(nil), obj...)
	} else {
		p = sc.ns.EncryptUnsafe(obj)
	}
	sc.wMx.Unlock()
	p = append(make([]byte, 2), p...)
	lps2, ok := safecast.To[uint16](len(p) - 2)
	if ok {
		binary.BigEndian.PutUint16(p, lps2)
		_, err := w.Write(p)
		return err
	}
	return fmt.Errorf("writeObject failed cast to uint16")
}

func (sc *SessionCommon) readObject(r io.Reader) (SignedObject, error) {
	lb := make([]byte, 2)
	if _, err := io.ReadFull(r, lb); err != nil {
		return nil, err
	}
	pb := make([]byte, binary.BigEndian.Uint16(lb))
	if _, err := io.ReadFull(r, pb); err != nil {
		return nil, err
	}

	// QUIC sessions (sc.ns == nil) read the signed object straight off the
	// already-encrypted QUIC stream — no Noise decrypt, no nonce window.
	if sc.ns == nil {
		return SignedObject(pb), nil
	}

	sc.rMx.Lock()
	if sc.nw == nil {
		sc.rMx.Unlock()
		return nil, ErrSessionClosed
	}
	obj, err := sc.ns.DecryptWithNonceWindow(sc.nw, pb)
	sc.rMx.Unlock()

	return obj, err
}

func (sc *SessionCommon) localSK() cipher.SecKey { return sc.entity.sk }

// LocalPK returns the local public key of the session.
func (sc *SessionCommon) LocalPK() cipher.PubKey { return sc.entity.pk }

// RemotePK returns the remote public key of the session.
func (sc *SessionCommon) RemotePK() cipher.PubKey { return sc.rPK }

// Carrier reports how this client session's byte pipe to the dmsg server was
// dialed: one of CarrierTCP / CarrierWS / CarrierWT / CarrierQUIC. It is empty
// for accepted (server-side) sessions. Use Protocol for a human-readable label
// that also distinguishes ws from wss.
func (sc *SessionCommon) Carrier() string { return sc.carrier }

// CarrierAddr reports the endpoint this session's carrier dialed. Empty for
// accepted (server-side) sessions.
func (sc *SessionCommon) CarrierAddr() string { return sc.carrierAddr }

// Protocol renders a human-readable label for the protocol this session used
// to reach its dmsg server, distinguishing plain ws:// from secured wss://.
func (sc *SessionCommon) Protocol() string { return ProtocolLabel(sc.carrier, sc.carrierAddr) }

// LocalTCPAddr returns the local address of the underlying TCP connection.
func (sc *SessionCommon) LocalTCPAddr() net.Addr { return sc.netConn.LocalAddr() }

// RemoteTCPAddr returns the remote address of the underlying TCP connection.
func (sc *SessionCommon) RemoteTCPAddr() net.Addr { return sc.netConn.RemoteAddr() }

// pingMarker is a 2-byte zero-length prefix that cannot occur in normal
// session traffic (valid SignedObjects always have length > 0). Used to
// implement ping over smux streams.
var pingMarker = []byte{0x00, 0x00}

// Ping obtains the round trip latency of the session.
func (sc *SessionCommon) Ping() (time.Duration, error) {
	// Snapshot the mux under the lock, then release it BEFORE the blocking
	// round-trip. Holding RLock across the up-to-sessionPingTimeout (18s)
	// round-trip stalled every sm.mutx.Lock() writer — Close / ForceReconnect
	// blocked up to 18s behind a single in-flight ping. Operating on the
	// snapshot is safe: if the session is closed concurrently, OpenStream /
	// Write / Read on the snapshotted mux just error out and the ping fails.
	sc.sm.mutx.RLock()
	yamuxSes := sc.sm.yamux
	smuxSes := sc.sm.smux
	sc.sm.mutx.RUnlock()
	if yamuxSes != nil {
		return sc.yamuxPing(yamuxSes)
	}
	if smuxSes != nil {
		return sc.smuxPing(smuxSes)
	}
	return 0, fmt.Errorf("no mux session available for ping")
}

// yamuxPing implements ping over yamux the same way smuxPing does for smux:
// open a temporary stream, write the ping marker, and wait for the echo.
//
// Why NOT yamux.Session.Ping(): that is a yamux *control-frame* keepalive —
// it round-trips at the mux-control layer over the existing TCP connection
// and proves only that the client<->server link is alive. It does NOT
// exercise the server's stream-forwarding path, so it cannot detect a
// "zombie" session where the control link is healthy but the dmsg server can
// no longer relay streams to/from us (observed in production: a dial-target
// service — e.g. a route setup-node behind a Docker bridge — kept advertising
// 8 "delegated servers" on which 0/8 stream-dials succeeded, while
// yamux.Ping() reported all 8 alive, so pingSessionsLoop never marked them
// dead and the node stayed unreachable until a manual restart). Opening a real
// stream and round-tripping the ping marker through the server's serveStream
// echo (which handles yamux and smux streams identically) exposes exactly that
// mismatch, so the existing pingDeadThreshold logic can close + redial the
// dead session. The smux path already worked this way; this brings yamux to
// parity.
// sessionPingTimeout bounds the liveness-ping round-trip. It must distinguish
// a DEAD session from a merely SLOW one: the ping opens a real stream and
// round-trips a marker through the server's serveStream path, so under load
// (or Docker's bridged-network latency) a live session's echo can take several
// seconds. The old 5s was low enough that, during a fleet reconnect storm or
// cold-start, live sessions false-positive as dead (pingDeadThreshold=2 → ~2
// failed pings → session killed → re-dial), manufacturing MORE reconnect churn
// — which floods the dmsg-server accept queues. 18s sits well above worst-case
// loaded round-trips yet well under the 60s ping interval, so a genuinely dead
// session is still caught within ~2 cycles. Slow sessions are already
// deprioritized for dials via RTT-based selection, so there's no benefit to
// culling them — only the harm of an extra noise handshake.
const sessionPingTimeout = 18 * time.Second

func (sc *SessionCommon) yamuxPing(yamuxSes *yamux.Session) (time.Duration, error) {
	str, err := yamuxSes.OpenStream()
	if err != nil {
		return 0, fmt.Errorf("yamux ping: open stream: %w", err)
	}
	defer str.Close() //nolint:errcheck

	if err := str.SetDeadline(time.Now().Add(sessionPingTimeout)); err != nil {
		return 0, fmt.Errorf("yamux ping: set deadline: %w", err)
	}

	start := time.Now()
	if _, err := str.Write(pingMarker); err != nil {
		return 0, fmt.Errorf("yamux ping: write: %w", err)
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(str, resp); err != nil {
		return 0, fmt.Errorf("yamux ping: read: %w", err)
	}
	return time.Since(start), nil
}

// smuxPing implements ping over smux by opening a temporary stream,
// writing a ping marker, and waiting for the echo.
func (sc *SessionCommon) smuxPing(smuxSes *smux.Session) (time.Duration, error) {
	str, err := smuxSes.OpenStream()
	if err != nil {
		return 0, fmt.Errorf("smux ping: open stream: %w", err)
	}
	defer str.Close() //nolint:errcheck

	if err := str.SetDeadline(time.Now().Add(sessionPingTimeout)); err != nil {
		return 0, fmt.Errorf("smux ping: set deadline: %w", err)
	}

	start := time.Now()
	if _, err := str.Write(pingMarker); err != nil {
		return 0, fmt.Errorf("smux ping: write: %w", err)
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(str, resp); err != nil {
		return 0, fmt.Errorf("smux ping: read: %w", err)
	}
	return time.Since(start), nil
}

// LastPing returns the last measured round-trip latency.
// Returns 0 if no measurement has been taken yet.
func (sc *SessionCommon) LastPing() time.Duration {
	return time.Duration(sc.lastPingNs.Load())
}

// SetLastPing records a latency measurement.
func (sc *SessionCommon) SetLastPing(d time.Duration) {
	sc.lastPingNs.Store(int64(d))
}

// Close closes the session.
func (sc *SessionCommon) Close() error {
	if sc == nil {
		return nil
	}
	var err error
	sc.sm.mutx.Lock()
	switch {
	case sc.sm.smux != nil:
		err = sc.sm.smux.Close()
	case sc.sm.yamux != nil:
		err = sc.sm.yamux.Close()
	case sc.sm.quic != nil:
		err = sc.sm.quic.CloseWithError(0, "session closed")
	}
	sc.sm.mutx.Unlock()
	sc.rMx.Lock()
	sc.nw = nil
	sc.rMx.Unlock()
	return err
}
