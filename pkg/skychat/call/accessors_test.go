// Package call pkg/skychat/call/accessors_test.go
//
// The small state accessors voice_test.go's end-to-end flows never touch:
// the per-session mute flags, Manager.SetMute that drives them, the
// late-listener hooks, and the signal-type label used in decline/busy errors.
//
// The mute pair is the only one here that guards user-facing behavior, and it
// guards a privacy-relevant one: SetMicMuted(false) means this visor's
// microphone is streaming to the peer. The rest are cheap correctness pins.
package call

import (
	"context"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestSessionMuteFlags(t *testing.T) {
	s := &Session{}

	// Default is UNMUTED both ways — a fresh call streams audio, which is why
	// the manual-answer gate exists upstream.
	if s.MicMuted() || s.SpeakerMuted() {
		t.Errorf("fresh session = mic %v speaker %v, want both unmuted", s.MicMuted(), s.SpeakerMuted())
	}

	s.SetMicMuted(true)
	if !s.MicMuted() {
		t.Error("SetMicMuted(true) should mute the mic")
	}
	if s.SpeakerMuted() {
		t.Error("muting the mic must not mute the speaker — they are independent")
	}

	s.SetSpeakerMuted(true)
	if !s.SpeakerMuted() {
		t.Error("SetSpeakerMuted(true) should mute the speaker")
	}

	s.SetMicMuted(false)
	if s.MicMuted() {
		t.Error("SetMicMuted(false) should unmute the mic")
	}
	if !s.SpeakerMuted() {
		t.Error("unmuting the mic must not unmute the speaker")
	}
}

// TestManagerSetMute — the /voice/mute endpoint proxies to this, so an unknown
// call id has to report rather than silently succeed (the UI would show the
// wrong mute state).
func TestManagerSetMute(t *testing.T) {
	m := NewManager(Config{})

	if err := m.SetMute("no-such-call", true, true); err == nil {
		t.Error("SetMute on an unknown call should error")
	}

	sess := &Session{}
	m.mu.Lock()
	m.calls["c-1"] = sess
	m.mu.Unlock()

	if err := m.SetMute("c-1", true, false); err != nil {
		t.Fatalf("SetMute: %v", err)
	}
	if !sess.MicMuted() || sess.SpeakerMuted() {
		t.Errorf("after SetMute(mic=true, speaker=false): mic %v speaker %v",
			sess.MicMuted(), sess.SpeakerMuted())
	}

	if err := m.SetMute("c-1", false, true); err != nil {
		t.Fatalf("SetMute: %v", err)
	}
	if sess.MicMuted() || !sess.SpeakerMuted() {
		t.Errorf("after SetMute(mic=false, speaker=true): mic %v speaker %v",
			sess.MicMuted(), sess.SpeakerMuted())
	}
}

func TestSigTypeName(t *testing.T) {
	cases := []struct {
		in   SigType
		want string
	}{
		{SigDecline, "declined"},
		{SigBusy, "busy"},
		{SigInvite, "sig(1)"}, // anything else falls through to the numeric form
		{SigType(99), "sig(99)"},
	}
	for _, c := range cases {
		if got := sigTypeName(c.in); got != c.want {
			t.Errorf("sigTypeName(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestAddListener_LateBinding — the skynet listener comes up after the dmsg one,
// so both the Signaler and the voice Manager must be able to take an extra
// listener once Serve is already running. A nil listener is a no-op.
func TestAddListener_LateBinding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pk, _ := cipher.GenerateKeyPair()
	sig := NewSignaler(pk, 1, nil, nil)
	sig.AddListener(ctx, nil) // must not panic or register anything
	lis := newMemListener()
	sig.AddListener(ctx, lis)

	sig.mu.Lock()
	n := len(sig.serving)
	sig.mu.Unlock()
	if n != 1 {
		t.Errorf("Signaler.serving = %d, want 1 (the nil listener is skipped)", n)
	}

	m := NewManager(Config{})
	m.AddListener(ctx, newMemListener())
	m.sig.mu.Lock()
	n = len(m.sig.serving)
	m.sig.mu.Unlock()
	if n != 1 {
		t.Errorf("Manager.AddListener did not reach the Signaler; serving = %d", n)
	}

	// Closing the listener unwinds the accept loop.
	_ = lis.Close() //nolint:errcheck
	time.Sleep(50 * time.Millisecond)
}
