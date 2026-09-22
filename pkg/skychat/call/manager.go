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

// RingTimeout is how long an unanswered inbound call rings, and therefore the
// longest a caller can be kept waiting for an answer. Exported because the
// caller's own budget has to be derived from it rather than guessed: the two
// were independent 30s and 45s constants, so a caller gave up fifteen seconds
// before the callee stopped ringing — the phone went on ringing for a call
// nobody was on the other end of any more.
const RingTimeout = ringTimeout

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
	// dialing holds calls THIS visor is placing, from the moment an id
	// exists until the peer answers or the invite fails. Without it a call
	// being dialed is invisible: it is in neither Incoming (that is the
	// callee's list) nor Active (that starts at "answered"), so a UI had
	// nothing to show for the ten seconds a caller most wants feedback.
	dialing map[string]*dialingCall
}

// dialingCall is an outbound invite in flight.
type dialingCall struct {
	peer cipher.PubKey
	// cancel aborts the invite. It is what makes hanging up DURING the ring
	// possible — there is no session to close yet.
	cancel context.CancelFunc
}

// Dial is one call this visor is placing, for a UI to show as "calling…".
type Dial struct {
	CallID string
	Peer   cipher.PubKey
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
	m := &Manager{cfg: cfg, log: cfg.Logger, calls: make(map[string]*Session), ringing: make(map[string]*ringingCall), taps: make(map[string]*callTap), dialing: make(map[string]*dialingCall)}
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

// beginDial registers an outbound call before its invite goes out, so the very
// first poll of a UI already sees it — and, more to the point, has an id to
// cancel it with. Returns the id, the cancellable dial context, and the
// cleanup that deregisters it.
func (m *Manager) beginDial(ctx context.Context, peer cipher.PubKey) (string, context.Context, func()) {
	callID := newCallID()
	dctx, cancel := context.WithCancel(ctx) //nolint:gosec // cancel is called by the returned func
	m.mu.Lock()
	m.dialing[callID] = &dialingCall{peer: peer, cancel: cancel}
	m.mu.Unlock()
	return callID, dctx, func() {
		m.mu.Lock()
		delete(m.dialing, callID)
		m.mu.Unlock()
		cancel()
	}
}

// Call places an outbound call to peer and BLOCKS until it is answered,
// declined, or the ring budget elapses. For a caller that waits — the CLI.
// Anything answering an HTTP request wants [Dial].
func (m *Manager) Call(ctx context.Context, peer cipher.PubKey) (*Session, error) {
	callID, dctx, done := m.beginDial(ctx, peer)
	defer done()
	return m.dial(dctx, callID, peer)
}

// Dial places an outbound call and returns its id straight away, leaving the
// invite to complete in the background.
//
// This is what a UI needs. A call's id is the only handle on it — hanging up
// takes one — and until the callee answers, that id exists nowhere else: the
// call is in no active list and no ringing list. A caller that only learns the
// id when the call CONNECTS therefore cannot call off a call that is still
// ringing, which is the one moment anyone wants to.
//
// It also has to not block. The two HTTP surfaces that place calls both cap a
// request well below the ring — skychat's own server at a 10s write timeout,
// the hypervisor's /api at 30s — so a handler that waited out a 55s ring could
// never deliver its answer to anyone. The caller saw a failed request for a
// call that was ringing perfectly well.
//
// It takes a budget rather than a context because it owns the call's whole
// lifetime: there is no request to tie it to, and tying it to one would end
// the call when the request did.
func (m *Manager) Dial(peer cipher.PubKey, budget time.Duration) string {
	ctx, cancel := context.WithTimeout(context.Background(), budget) //nolint:gosec // canceled by the goroutine below
	callID, dctx, done := m.beginDial(ctx, peer)
	go func() {
		defer cancel()
		defer done()
		if _, err := m.dial(dctx, callID, peer); err != nil {
			m.log.WithError(err).WithField("call", callID).
				Info("voice: outbound call ended before it connected")
		}
	}()
	return callID
}

// dial sends the invite and, on accept, starts the media session.
func (m *Manager) dial(ctx context.Context, callID string, peer cipher.PubKey) (*Session, error) {
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
		// Watched, so that a caller hanging up mid-ring stops the ring here
		// and now instead of leaving it to run out the timeout. Everything
		// downstream reads through the watch — see ringWatch.
		w := watchRing(conn)
		switch m.ringAndWait(inv, w.gone) {
		case ringAnswered:
			w.answer()
			m.accept(inv, w)
		case ringAbandoned:
			// Nobody to decline to: the caller has already hung up, and the
			// write would only block on a conn that is on its way out.
			_ = w.Close() //nolint:errcheck
		default:
			_ = writeSig(w, Sig{Type: SigDecline, CallID: inv.CallID, FromPK: m.cfg.LocalPK, Reason: "no answer"}) //nolint:errcheck
			_ = w.Close()                                                                                          //nolint:errcheck
		}
		return
	}
	if m.cfg.OnIncoming == nil || !m.cfg.OnIncoming(inv) {
		_ = writeSig(conn, Sig{Type: SigDecline, CallID: inv.CallID, FromPK: m.cfg.LocalPK, Reason: "declined"}) //nolint:errcheck
		_ = conn.Close()                                                                                         //nolint:errcheck
		return
	}
	m.accept(inv, conn)
}

// ringOutcome is how a ring ended.
type ringOutcome int

const (
	// ringAnswered: someone picked up.
	ringAnswered ringOutcome = iota
	// ringDeclined: an explicit decline, or the ring timing out — either way
	// the caller is still there and is owed an answer.
	ringDeclined
	// ringAbandoned: the caller hung up before anyone picked up.
	ringAbandoned
)

// ringAndWait parks the invite as a ringing call and blocks until it's
// answered, declined, abandoned by the caller, or the ring timeout fires.
//
// gone is the caller-hung-up signal from the call's ringWatch. It is what
// makes a ring end when the call does rather than when the timeout says so.
func (m *Manager) ringAndWait(inv Sig, gone <-chan struct{}) ringOutcome {
	rc := &ringingCall{inv: inv, decided: make(chan bool, 1)}
	m.mu.Lock()
	m.ringing[inv.CallID] = rc
	m.mu.Unlock()
	if m.cfg.Ring != nil {
		m.cfg.Ring(inv)
	}
	m.log.WithField("from", inv.FromPK.Hex()).WithField("call", inv.CallID).
		Info("voice: incoming call RINGING — answer with `skychat voice answer <id>`")

	timeout := time.NewTimer(ringTimeout)
	defer timeout.Stop()
	out := ringDeclined
	select {
	case ok := <-rc.decided:
		if ok {
			out = ringAnswered
		}
	case <-gone:
		out = ringAbandoned
	case <-timeout.C:
	}
	m.mu.Lock()
	delete(m.ringing, inv.CallID)
	m.mu.Unlock()
	if out == ringAbandoned {
		m.log.WithField("from", inv.FromPK.Hex()).WithField("call", inv.CallID).
			Info("voice: caller hung up before the call was answered")
	}
	return out
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

// Dialing returns the calls this visor is placing and that have not been
// answered yet.
func (m *Manager) Dialing() []Dial {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Dial, 0, len(m.dialing))
	for id, d := range m.dialing {
		out = append(out, Dial{CallID: id, Peer: d.peer})
	}
	return out
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
		// With the reason, because "call ended" on its own is what a report
		// of calls dropping by themselves has to be diagnosed from, and it
		// says nothing: a peer hanging up and a transport collapsing under a
		// live call produced the identical line.
		reason := sess.EndReason()
		if reason == "" {
			reason = "hung up here"
		}
		m.log.WithField("call", callID).WithField("reason", reason).Info("voice: call ended")
	}
}

// Hangup ends an active call by id (closes its media conn; the peer sees EOF).
func (m *Manager) Hangup(callID string) error {
	m.mu.Lock()
	sess := m.calls[callID]
	dial := m.dialing[callID]
	m.mu.Unlock()
	if sess != nil {
		sess.Close()
		return nil
	}
	// Still ringing at the other end: there is no session to close, so
	// canceling the invite IS the hang-up. Without this the caller could
	// only wait out the dial timeout.
	if dial != nil {
		dial.cancel()
		return nil
	}
	return errors.New("voice: no such call")
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
