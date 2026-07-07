// Package voice pkg/skychat/voice/manager.go c2-app-chat
package voice

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// Config wires a Manager. Dial + the listeners are supplied by the runtime
// (native visor or wasm) so this package stays transport-agnostic (KG7): both a
// dmsg client and a skynet networker satisfy them, and voice signals on the SAME
// port over both.
type Config struct {
	LocalPK cipher.PubKey
	// Dial opens a stream to peer:port over an available skywire network.
	Dial DialFunc
	// SignalPort / MediaPort default to the skyenv voice ports when zero.
	SignalPort uint16
	// Codec defaults to the PCM passthrough (Opus is a cgo follow-up).
	Codec Codec
	// NewSource / NewSink build the mic / speaker for a call. Default to
	// SilentSource / NullSink (headless) so the control+media plane runs without
	// audio hardware; a real audio backend swaps these in.
	NewSource func() Source
	NewSink   func() Sink
	// OnIncoming decides whether to accept an inbound invite. nil => decline all
	// (voice off). A UI wires this to a ring/accept prompt; a headless/test peer
	// can return true to auto-answer.
	OnIncoming func(inv Sig) bool
	Logger     *logging.Logger
}

// Manager owns the local voice endpoint: it accepts inbound calls via the
// Signaler and places outbound calls, holding one Session per active call.
type Manager struct {
	cfg Config
	sig *Signaler
	log *logging.Logger

	mu    sync.Mutex
	calls map[string]*Session
}

// NewManager constructs a Manager. Call Serve to start accepting.
func NewManager(cfg Config) *Manager {
	if cfg.Logger == nil {
		cfg.Logger = logging.MustGetLogger("voice")
	}
	if cfg.Codec == nil {
		cfg.Codec = NewPCMCodec()
	}
	if cfg.NewSource == nil {
		cfg.NewSource = func() Source { return SilentSource{} }
	}
	if cfg.NewSink == nil {
		cfg.NewSink = func() Sink { return NullSink{} }
	}
	m := &Manager{cfg: cfg, log: cfg.Logger, calls: make(map[string]*Session)}
	m.sig = NewSignaler(cfg.LocalPK, cfg.SignalPort, cfg.Dial, cfg.Logger)
	m.sig.SetInviteHandler(m.handleInvite)
	return m
}

// Serve accepts inbound call signaling on the given listeners — pass BOTH the
// dmsg listener and the skynet listener bound to SignalPort so callers reach us
// over whichever network is up. Blocks until ctx is done.
func (m *Manager) Serve(ctx context.Context, listeners ...net.Listener) {
	m.sig.Serve(ctx, listeners...)
}

// AddListener joins an additional signaling listener after Serve has started
// (used for the skynet listener, which comes up after the dmsg one).
func (m *Manager) AddListener(ctx context.Context, lis net.Listener) {
	m.sig.AddListener(ctx, lis)
}

// Call places an outbound call to peer: invite over signaling, and on accept
// start a media Session over the (now media-mode) conn. Returns the live
// Session, or the peer's decline/busy reason.
func (m *Manager) Call(ctx context.Context, peer cipher.PubKey) (*Session, error) {
	callID := newCallID()
	conn, reply, err := m.sig.Invite(ctx, peer, callID, m.cfg.Codec.Name(), m.cfg.SignalPort)
	if err != nil {
		return nil, err
	}
	if reply.Type != SigAccept {
		reason := reply.Reason
		if reason == "" {
			reason = sigTypeName(reply.Type)
		}
		return nil, fmt.Errorf("voice: call not accepted: %s", reason)
	}
	sess := m.startSession(callID, conn, ssrcFromPK(m.cfg.LocalPK))
	// The session runs independently of the (possibly short) invite ctx — it
	// ends when the conn closes (Hangup or the peer hanging up), not when the
	// caller's dial deadline elapses.
	go func() { sess.Run(context.Background()); m.dropCall(callID) }() //nolint:gosec // session outlives the invite/request ctx by design
	return sess, nil
}

// handleInvite is the callee side: decide accept/decline, and on accept reply
// SigAccept and start a media Session over the same conn.
func (m *Manager) handleInvite(inv Sig, conn net.Conn) {
	accept := m.cfg.OnIncoming != nil && m.cfg.OnIncoming(inv)
	if !accept {
		_ = writeSig(conn, Sig{Type: SigDecline, CallID: inv.CallID, FromPK: m.cfg.LocalPK, Reason: "declined"}) //nolint:errcheck
		_ = conn.Close()                                                                                         //nolint:errcheck
		return
	}
	ack := Sig{Type: SigAccept, CallID: inv.CallID, FromPK: m.cfg.LocalPK, Codec: m.cfg.Codec.Name(), MediaPort: m.cfg.SignalPort}
	if err := writeSig(conn, ack); err != nil {
		_ = conn.Close() //nolint:errcheck
		return
	}
	sess := m.startSession(inv.CallID, conn, ssrcFromPK(m.cfg.LocalPK))
	go func() { sess.Run(context.Background()); m.dropCall(inv.CallID) }() //nolint:gosec // session outlives the invite/request ctx by design
}

func (m *Manager) startSession(callID string, conn net.Conn, ssrc uint32) *Session {
	sess := NewSession(callID, conn, m.cfg.Codec, m.cfg.NewSource(), m.cfg.NewSink(), ssrc, m.log)
	m.mu.Lock()
	m.calls[callID] = sess
	m.mu.Unlock()
	m.log.WithField("call", callID).Info("voice: call established")
	return sess
}

func (m *Manager) dropCall(callID string) {
	m.mu.Lock()
	sess := m.calls[callID]
	delete(m.calls, callID)
	m.mu.Unlock()
	if sess != nil {
		sess.Close()
		m.log.WithField("call", callID).Info("voice: call ended")
	}
}

// Hangup ends an active call by id (closes its media conn; the peer sees EOF).
func (m *Manager) Hangup(callID string) error {
	m.mu.Lock()
	sess := m.calls[callID]
	m.mu.Unlock()
	if sess == nil {
		return errors.New("voice: no such call")
	}
	sess.Close()
	return nil
}

// Active returns the ids of live calls.
func (m *Manager) Active() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.calls))
	for id := range m.calls {
		ids = append(ids, id)
	}
	return ids
}

func newCallID() string {
	var b [8]byte
	_, _ = rand.Read(b[:]) //nolint:errcheck
	return hex.EncodeToString(b[:])
}

// ssrcFromPK derives a stable RTP SSRC from the sender PK (first 4 bytes).
func ssrcFromPK(pk cipher.PubKey) uint32 {
	return binary.BigEndian.Uint32(pk[1:5])
}

func sigTypeName(t SigType) string {
	switch t {
	case SigDecline:
		return "declined"
	case SigBusy:
		return "busy"
	default:
		return fmt.Sprintf("sig(%d)", t)
	}
}
