// Package call pkg/skychat/call/manager.go c4-app-chat
package call

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// ringTimeout bounds how long an unanswered inbound call rings before it's
// auto-declined (in ManualAnswer mode).
const ringTimeout = 45 * time.Second

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
	// Codec is the template codec, used for the codec NAME in signaling. Defaults
	// to the PCM passthrough.
	Codec Codec
	// NewCodec builds a fresh codec per session. A stateful codec (Opus keeps
	// encoder/decoder state) MUST NOT be shared across concurrent calls, so each
	// session gets its own. Defaults to returning the (stateless) Codec.
	NewCodec func() Codec
	// NewSource / NewSink build the mic / speaker for a call. Default to
	// SilentSource / NullSink (headless) so the control+media plane runs without
	// audio hardware; a real audio backend swaps these in.
	NewSource func() Source
	NewSink   func() Sink
	// OnIncoming decides whether to accept an inbound invite IMMEDIATELY (used in
	// auto-answer mode). nil => decline all (voice off). A headless/test peer
	// returns true to auto-answer. Ignored when ManualAnswer is set.
	OnIncoming func(inv Sig) bool
	// ManualAnswer, when true, makes an inbound invite RING (park, pending) until
	// Answer(callID) or Decline(callID) — never auto-accepting. This is the mode
	// used once real mic capture is enabled, so a visor never streams its
	// microphone to a caller without an explicit answer. Ring (optional) is
	// notified when a call starts ringing.
	ManualAnswer bool
	Ring         func(inv Sig)
	// Visualize, when true, taps each call's sent/received PCM into a small ring
	// so CallAudio can serve it for a live spectrogram. The audio is unaffected.
	Visualize bool
	Logger    *logging.Logger
}

// ringingCall is an inbound invite parked awaiting an explicit answer/decline.
type ringingCall struct {
	inv     Sig
	decided chan bool // buffered(1): true=answer, false=decline
}

// Manager owns the local voice endpoint: it accepts inbound calls via the
// Signaler and places outbound calls, holding one Session per active call.
type Manager struct {
	cfg Config
	sig *Signaler
	log *logging.Logger

	mu      sync.Mutex
	calls   map[string]*Session
	ringing map[string]*ringingCall
	taps    map[string]*callTap
}

// NewManager constructs a Manager. Call Serve to start accepting.
func NewManager(cfg Config) *Manager {
	if cfg.Logger == nil {
		cfg.Logger = logging.MustGetLogger("voice")
	}
	if cfg.NewCodec == nil {
		if cfg.Codec == nil {
			cfg.Codec = NewPCMCodec()
		}
		tmpl := cfg.Codec
		cfg.NewCodec = func() Codec { return tmpl } // stateless PCM: share the instance
	} else if cfg.Codec == nil {
		cfg.Codec = cfg.NewCodec() // derive the template for the signaling name
	}
	if cfg.NewSource == nil {
		cfg.NewSource = func() Source { return SilentSource{} }
	}
	if cfg.NewSink == nil {
		cfg.NewSink = func() Sink { return NullSink{} }
	}
	m := &Manager{cfg: cfg, log: cfg.Logger, calls: make(map[string]*Session), ringing: make(map[string]*ringingCall), taps: make(map[string]*callTap)}
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

// handleInvite is the callee side. In auto-answer mode it decides immediately
// via OnIncoming; in ManualAnswer mode it RINGS (parks the conn) until an
// explicit Answer/Decline or the ring timeout. On accept it replies SigAccept
// and starts a media Session over the same conn.
func (m *Manager) handleInvite(inv Sig, conn net.Conn) {
	if m.cfg.ManualAnswer {
		if !m.ringAndWait(inv) {
			_ = writeSig(conn, Sig{Type: SigDecline, CallID: inv.CallID, FromPK: m.cfg.LocalPK, Reason: "no answer"}) //nolint:errcheck
			_ = conn.Close()                                                                                          //nolint:errcheck
			return
		}
		m.accept(inv, conn)
		return
	}
	if m.cfg.OnIncoming == nil || !m.cfg.OnIncoming(inv) {
		_ = writeSig(conn, Sig{Type: SigDecline, CallID: inv.CallID, FromPK: m.cfg.LocalPK, Reason: "declined"}) //nolint:errcheck
		_ = conn.Close()                                                                                         //nolint:errcheck
		return
	}
	m.accept(inv, conn)
}

// ringAndWait parks the invite as a ringing call and blocks until it's answered,
// declined, or the ring timeout fires. Returns true only on an explicit answer.
func (m *Manager) ringAndWait(inv Sig) bool {
	rc := &ringingCall{inv: inv, decided: make(chan bool, 1)}
	m.mu.Lock()
	m.ringing[inv.CallID] = rc
	m.mu.Unlock()
	if m.cfg.Ring != nil {
		m.cfg.Ring(inv)
	}
	m.log.WithField("from", inv.FromPK.Hex()).WithField("call", inv.CallID).
		Info("voice: incoming call RINGING — answer with `skychat voice answer <id>`")

	var ok bool
	select {
	case ok = <-rc.decided:
	case <-time.After(ringTimeout):
	}
	m.mu.Lock()
	delete(m.ringing, inv.CallID)
	m.mu.Unlock()
	return ok
}

// accept replies SigAccept and starts the media session over conn.
func (m *Manager) accept(inv Sig, conn net.Conn) {
	ack := Sig{Type: SigAccept, CallID: inv.CallID, FromPK: m.cfg.LocalPK, Codec: m.cfg.Codec.Name(), MediaPort: m.cfg.SignalPort}
	if err := writeSig(conn, ack); err != nil {
		_ = conn.Close() //nolint:errcheck
		return
	}
	sess := m.startSession(inv.CallID, conn, ssrcFromPK(m.cfg.LocalPK))
	go func() { sess.Run(context.Background()); m.dropCall(inv.CallID) }() //nolint:gosec // session outlives the invite/request ctx by design
}

// Answer accepts a ringing inbound call by id (ManualAnswer mode).
func (m *Manager) Answer(callID string) error { return m.decide(callID, true) }

// Decline rejects a ringing inbound call by id.
func (m *Manager) Decline(callID string) error { return m.decide(callID, false) }

func (m *Manager) decide(callID string, ok bool) error {
	m.mu.Lock()
	rc := m.ringing[callID]
	m.mu.Unlock()
	if rc == nil {
		return errors.New("voice: no ringing call with that id")
	}
	select {
	case rc.decided <- ok:
	default: // already decided
	}
	return nil
}

// Incoming returns the invites of calls currently ringing (awaiting answer).
func (m *Manager) Incoming() []Sig {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Sig, 0, len(m.ringing))
	for _, rc := range m.ringing {
		out = append(out, rc.inv)
	}
	return out
}

func (m *Manager) startSession(callID string, conn net.Conn, ssrc uint32) *Session {
	src := m.cfg.NewSource()
	sink := m.cfg.NewSink()
	if m.cfg.Visualize {
		tap := &callTap{sent: newAudioRing(sampleRate), recv: newAudioRing(sampleRate)} // ~1s each
		src = &teeSource{inner: src, ring: tap.sent}
		sink = &teeSink{inner: sink, ring: tap.recv}
		m.mu.Lock()
		m.taps[callID] = tap
		m.mu.Unlock()
	}
	sess := NewSession(callID, conn, m.cfg.NewCodec(), src, sink, ssrc, m.log)
	m.mu.Lock()
	m.calls[callID] = sess
	m.mu.Unlock()
	m.log.WithField("call", callID).Info("voice: call established")
	return sess
}

// CallAudio returns the most recent buffered sent + received PCM for a call
// (Visualize mode). Used to drive a live spectrogram.
func (m *Manager) CallAudio(callID string) (sent, recv []int16, err error) {
	m.mu.Lock()
	tap := m.taps[callID]
	m.mu.Unlock()
	if tap == nil {
		return nil, nil, errors.New("voice: no visualized audio for that call")
	}
	return tap.sent.snapshot(), tap.recv.snapshot(), nil
}

func (m *Manager) dropCall(callID string) {
	m.mu.Lock()
	sess := m.calls[callID]
	delete(m.calls, callID)
	delete(m.taps, callID)
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

// SetMute toggles the mic (send) and speaker (playback) mute state of an active
// call. mic=true silences what the peer hears from us; speaker=true silences
// what we hear from the peer ("mute the caller").
func (m *Manager) SetMute(callID string, mic, speaker bool) error {
	m.mu.Lock()
	sess := m.calls[callID]
	m.mu.Unlock()
	if sess == nil {
		return errors.New("voice: no such call")
	}
	sess.SetMicMuted(mic)
	sess.SetSpeakerMuted(speaker)
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
