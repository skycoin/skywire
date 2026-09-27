// Package call pkg/skychat/call/progress.go c4-app-chat
package call

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// DialState is where an outbound call has got to, as the caller sees it.
//
// A caller used to see one state for the whole of a ring: "calling…". A peer
// that was not on the network, a peer whose phone was ringing, and a peer who
// had just pressed decline all looked the same — a banner that sat there and
// then went away — so nobody placing a call could tell whether to wait, try
// again later, or give up.
type DialState string

const (
	// DialConnecting: the invite has not reached the peer yet — the network
	// is still looking for them.
	DialConnecting DialState = "connecting"
	// DialCalling: the peer has the invite but has said nothing since. What
	// a peer that predates SigRinging looks like for the whole ring.
	DialCalling DialState = "calling"
	// DialRinging: the peer says it is ringing.
	DialRinging DialState = "ringing"

	// DialOffline: the peer could not be reached at all.
	DialOffline DialState = "offline"
	// DialDeclined: the peer turned the call down.
	DialDeclined DialState = "declined"
	// DialBusy: the peer is on another call and said so.
	DialBusy DialState = "busy"
	// DialNoAnswer: it rang out.
	DialNoAnswer DialState = "no_answer"
	// DialFailed: it ended some other way; Dial.Reason says how.
	DialFailed DialState = "failed"
)

// Ended reports whether the call is over — every state past ringing is an
// outcome rather than progress.
func (s DialState) Ended() bool {
	switch s {
	case DialConnecting, DialCalling, DialRinging:
		return false
	}
	return true
}

// DialOutcomeLinger is how long an outbound call that ended unanswered stays
// in Dialing with its outcome.
//
// The outcome is the point of the feature, and it is exactly what vanished:
// the call left the list the moment it ended, so a UI polling every couple of
// seconds found an empty list and could only guess why. Long enough for two
// polls to see it; short enough that a stale "offline" is not what greets the
// next call. A UI that has shown it can drop it early with Hangup.
const DialOutcomeLinger = 8 * time.Second

// announceGrace bounds waiting for the SigRinging write before the answer is
// written behind it. The frame is a couple of hundred bytes on an idle conn, so
// this only ever runs out on a conn that is wedged, which is then closed —
// writing the answer anyway could interleave it with a frame still in flight.
const announceGrace = 2 * time.Second

// setDialState moves a live outbound call forward. An ended call stays ended:
// a late hook cannot resurrect it.
func (m *Manager) setDialState(callID string, state DialState) {
	m.mu.Lock()
	if d := m.dialing[callID]; d != nil && !d.state.Ended() {
		d.state = state
	}
	m.mu.Unlock()
}

// endDial records how an unanswered outbound call ended and keeps it listed
// for DialOutcomeLinger, so the UIs polling Dialing can say why.
func (m *Manager) endDial(callID string, state DialState, reason string) {
	m.mu.Lock()
	d := m.dialing[callID]
	if d == nil || d.state.Ended() {
		m.mu.Unlock()
		return
	}
	d.state, d.reason = state, reason
	m.mu.Unlock()
	m.log.WithField("call", callID).WithField("outcome", string(state)).WithField("reason", reason).
		Info("voice: outbound call not connected")
	time.AfterFunc(DialOutcomeLinger, func() { m.forgetDial(callID, d) })
}

// forgetDial drops an ended outbound call — only the one it was asked about:
// the id is random, but checking the entry is cheaper than reasoning about it.
func (m *Manager) forgetDial(callID string, d *dialingCall) {
	m.mu.Lock()
	if m.dialing[callID] == d {
		delete(m.dialing, callID)
	}
	m.mu.Unlock()
}

// dialErrOutcome maps a failed invite to what the caller is told.
//
// A call the caller called off is no outcome at all: the UI that hung up has
// already moved on, so there is nothing to report and ok is false.
func dialErrOutcome(ctx context.Context, state DialState, err error) (DialState, string, bool) {
	if errors.Is(ctx.Err(), context.Canceled) {
		return "", "", false
	}
	if errors.Is(err, ErrUnreachable) {
		return DialOffline, err.Error(), true
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		if state == DialConnecting {
			return DialOffline, "the peer could not be reached in time", true
		}
		return DialNoAnswer, "no answer", true
	}
	return DialFailed, err.Error(), true
}

// replyOutcome maps a peer's refusal to what the caller is told.
//
// "no answer" is the ring timing out at the callee. A callee that predates
// this also said it for an explicit decline, so from one of those every
// refusal reads as no answer, which is the kinder of the two to be wrong about.
func replyOutcome(reply Sig) (DialState, string) {
	switch reply.Type {
	case SigBusy:
		return DialBusy, "busy"
	case SigDecline:
		switch reply.Reason {
		case "no answer":
			return DialNoAnswer, reply.Reason
		case "", "declined":
			return DialDeclined, "declined"
		}
		return DialFailed, reply.Reason
	}
	return DialFailed, sigTypeName(reply.Type)
}

// announceRinging tells the caller this endpoint has started ringing, and
// which ringback tone it plays (see ringback.go). It returns at once; the
// channel closes when the write is done, and the answer must not be written
// before it is.
func (m *Manager) announceRinging(conn net.Conn, inv Sig) <-chan struct{} {
	sig := Sig{Type: SigRinging, CallID: inv.CallID, FromPK: m.cfg.LocalPK}
	m.mu.Lock()
	if t := m.ownTone; t != nil {
		sig.Tone, sig.ToneMime, sig.ToneSize = t.hash, t.mime, len(t.data)
	}
	m.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := writeSig(conn, sig); err != nil {
			m.log.WithError(err).WithField("call", inv.CallID).Debug("voice: could not tell the caller we are ringing")
		}
	}()
	return done
}

// announced waits for announceRinging's write, bounded. False means the conn
// is wedged mid-frame and must not be written to again.
func announced(done <-chan struct{}) bool {
	if done == nil {
		return true
	}
	timer := time.NewTimer(announceGrace)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// noteRinging records the callee's SigRinging on the outbound call it belongs
// to, and — when the callee plays a ringback tone this side does not hold yet —
// starts fetching it.
func (m *Manager) noteRinging(callID string, peer cipher.PubKey, r Sig) {
	fetch := false
	m.mu.Lock()
	d := m.dialing[callID]
	if d == nil || d.state.Ended() {
		m.mu.Unlock()
		return
	}
	d.state = DialRinging
	if r.Tone != "" && d.tone == "" {
		d.tone = r.Tone
		if t := m.tones[peer]; t != nil && t.hash == r.Tone {
			t.used = time.Now()
			d.toneReady = true
		} else if r.ToneSize > 0 && r.ToneSize <= MaxRingbackSize {
			fetch = true
		}
	}
	m.mu.Unlock()
	if fetch {
		go m.fetchTone(callID, peer, r)
	}
}
