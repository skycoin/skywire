// Package call pkg/skychat/call/progress_test.go
package call

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// dialOf is the listed outbound call id, or ok=false.
func dialOf(m *Manager, callID string) (Dial, bool) {
	for _, d := range m.Dialing() {
		if d.CallID == callID {
			return d, true
		}
	}
	return Dial{}, false
}

// waitDial polls until the outbound call satisfies cond, failing after within.
func waitDial(t *testing.T, m *Manager, callID string, within time.Duration, what string, cond func(Dial, bool) bool) Dial {
	t.Helper()
	deadline := time.After(within)
	for {
		d, ok := dialOf(m, callID)
		if cond(d, ok) {
			return d
		}
		select {
		case <-deadline:
			t.Fatalf("%s: outbound call is %+v (listed=%v) after %s", what, d, ok, within)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// ringingPair is a caller A and a ringing (explicit-answer) callee B joined
// by an in-memory listener. rang delivers each invite as B starts ringing.
func ringingPair(t *testing.T, ctx context.Context) (mgrA, mgrB *Manager, pkB cipher.PubKey, dialer *recordingDialer, rang chan Sig) {
	t.Helper()
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ = cipher.GenerateKeyPair()
	lis := newMemListener()
	t.Cleanup(func() { _ = lis.Close() }) //nolint:errcheck
	rang = make(chan Sig, 4)
	mgrB = NewManager(Config{
		LocalPK:      pkB,
		Dial:         neverDial,
		ManualAnswer: true,
		Ring:         func(inv Sig) { rang <- inv },
	})
	go mgrB.Serve(ctx, lis)
	dialer = &recordingDialer{lis: lis}
	mgrA = NewManager(Config{LocalPK: pkA, Dial: dialer.dial})
	return mgrA, mgrB, pkB, dialer, rang
}

func awaitRing(t *testing.T, rang chan Sig) Sig {
	t.Helper()
	select {
	case inv := <-rang:
		return inv
	case <-time.After(2 * time.Second):
		t.Fatal("call never rang")
	}
	return Sig{}
}

// TestDialReportsRingingThenDeclined: a caller sees the callee start ringing,
// and after a decline sees that it was declined — the two things the old
// single "calling…" could not say.
func TestDialReportsRingingThenDeclined(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	mgrA, mgrB, pkB, _, rang := ringingPair(t, ctx)

	callID := mgrA.Dial(pkB, RingTimeout+10*time.Second)
	inv := awaitRing(t, rang)
	if !inv.Progress {
		t.Fatal("the invite did not ask for ringing progress")
	}
	d := waitDial(t, mgrA, callID, 2*time.Second, "ringing", func(d Dial, ok bool) bool { return ok && d.State == DialRinging })
	if d.Ringback {
		t.Fatal("a callee with no ringback tone reported one")
	}

	if err := mgrB.Decline(inv.CallID); err != nil {
		t.Fatalf("Decline: %v", err)
	}
	d = waitDial(t, mgrA, callID, 2*time.Second, "declined", func(d Dial, ok bool) bool { return ok && d.State.Ended() })
	if d.State != DialDeclined {
		t.Fatalf("outcome %q (%s), want declined", d.State, d.Reason)
	}

	// The outcome lingers for a UI to show, and the UI dismissing it ends
	// that at once.
	time.Sleep(100 * time.Millisecond)
	if _, ok := dialOf(mgrA, callID); !ok {
		t.Fatal("the outcome vanished before a UI could see it")
	}
	if err := mgrA.Hangup(callID); err != nil {
		t.Fatalf("Hangup (dismiss): %v", err)
	}
	if _, ok := dialOf(mgrA, callID); ok {
		t.Fatal("the dismissed outcome is still listed")
	}
}

// TestDialAnsweredLeavesTheList: a call that connects is the active list's
// from then on, not the dialing list's — outcome lingering is for calls that
// did not connect.
func TestDialAnsweredLeavesTheList(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	mgrA, mgrB, pkB, _, rang := ringingPair(t, ctx)

	callID := mgrA.Dial(pkB, RingTimeout+10*time.Second)
	inv := awaitRing(t, rang)
	waitDial(t, mgrA, callID, 2*time.Second, "ringing", func(d Dial, ok bool) bool { return ok && d.State == DialRinging })
	if err := mgrB.Answer(inv.CallID); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	waitDial(t, mgrA, callID, 2*time.Second, "answered", func(_ Dial, ok bool) bool { return !ok })
	if got := mgrA.Active(); len(got) != 1 || got[0] != callID {
		t.Fatalf("Active() = %v, want [%s]", got, callID)
	}
	_ = mgrA.Hangup(callID) //nolint:errcheck
}

// TestDialOffline: a peer the network cannot reach is reported offline, not
// left as a call that quietly stops existing.
func TestDialOffline(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	mgrA := NewManager(Config{
		LocalPK: pkA,
		Dial: func(context.Context, cipher.PubKey, uint16) (net.Conn, error) {
			return nil, errors.New("dmsg error 100 - entry is not found in discovery")
		},
	})
	callID := mgrA.Dial(pkB, 5*time.Second)
	d := waitDial(t, mgrA, callID, 2*time.Second, "offline", func(d Dial, ok bool) bool { return ok && d.State.Ended() })
	if d.State != DialOffline {
		t.Fatalf("outcome %q (%s), want offline", d.State, d.Reason)
	}
}

// TestCallerHangupIsNoOutcome: a caller who hangs up mid-ring gets nothing
// listed afterwards — the UI that hung up has moved on, and an "outcome" of
// its own cancel would only resurface as a stale banner.
func TestCallerHangupIsNoOutcome(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	mgrA, _, pkB, _, rang := ringingPair(t, ctx)

	callID := mgrA.Dial(pkB, RingTimeout+10*time.Second)
	awaitRing(t, rang)
	waitDial(t, mgrA, callID, 2*time.Second, "ringing", func(d Dial, ok bool) bool { return ok && d.State == DialRinging })
	if err := mgrA.Hangup(callID); err != nil {
		t.Fatalf("Hangup: %v", err)
	}
	waitDial(t, mgrA, callID, 2*time.Second, "gone after hangup", func(_ Dial, ok bool) bool { return !ok })
}

// TestOldCalleeStaysCalling: a callee that predates SigRinging never says it
// is ringing, so the caller shows "calling" — the invite got there — rather
// than being stuck at "connecting".
func TestOldCalleeStaysCalling(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	lis := newMemListener()
	defer lis.Close() //nolint:errcheck
	// The old callee: reads the invite and says nothing, as it did while
	// ringing.
	go func() {
		c, err := lis.Accept()
		if err != nil {
			return
		}
		_, _ = readSig(c) //nolint:errcheck
		_, _ = io.Copy(io.Discard, c)
	}()
	mgrA := NewManager(Config{LocalPK: pkA, Dial: func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return lis.dial() }})
	callID := mgrA.Dial(pkB, RingTimeout+10*time.Second)
	waitDial(t, mgrA, callID, 2*time.Second, "calling", func(d Dial, ok bool) bool { return ok && d.State == DialCalling })
	_ = mgrA.Hangup(callID) //nolint:errcheck
}

// TestOldCallerGetsNoRinging: an invite that did not ask for progress gets no
// SigRinging. A caller that predates it would read one as the reply and end
// the call on it.
func TestOldCallerGetsNoRinging(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	lis := newMemListener()
	defer lis.Close() //nolint:errcheck
	rang := make(chan Sig, 1)
	mgrB := NewManager(Config{LocalPK: pkB, Dial: neverDial, ManualAnswer: true, Ring: func(inv Sig) { rang <- inv }})
	_ = mgrB.SetRingback([]byte("tone"), "audio/mpeg") //nolint:errcheck
	go mgrB.Serve(ctx, lis)

	c, err := lis.dial()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close() //nolint:errcheck
	if err := writeSig(c, Sig{Type: SigInvite, CallID: "old", FromPK: pkA, Codec: "pcm"}); err != nil {
		t.Fatal(err)
	}
	awaitRing(t, rang)
	if err := mgrB.Decline("old"); err != nil {
		t.Fatal(err)
	}
	first, err := readSig(c)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if first.Type != SigDecline {
		t.Fatalf("an old caller's first frame is %s, want the decline", sigTypeName(first.Type))
	}
}

// TestRingbackToneReachesTheCaller: the callee's own tone is fetched while it
// rings and handed to the caller's UI — and the next call to the same peer
// plays it from the cache instead of fetching it again.
func TestRingbackToneReachesTheCaller(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	mgrA, mgrB, pkB, dialer, rang := ringingPair(t, ctx)
	// Past one chunk, so the reassembly is exercised.
	want := bytes.Repeat([]byte("ringback!"), (toneChunk*2+123)/9)
	if err := mgrB.SetRingback(want, "audio/mpeg"); err != nil {
		t.Fatalf("SetRingback: %v", err)
	}

	for round := 1; round <= 2; round++ {
		before := dialer.count()
		callID := mgrA.Dial(pkB, RingTimeout+10*time.Second)
		inv := awaitRing(t, rang)
		waitDial(t, mgrA, callID, 3*time.Second, fmt.Sprintf("round %d: tone ready", round),
			func(d Dial, ok bool) bool { return ok && d.Ringback })
		got, mime, ok := mgrA.DialRingback(callID)
		if !ok || !bytes.Equal(got, want) || mime != "audio/mpeg" {
			t.Fatalf("round %d: DialRingback = %d bytes %q ok=%v, want %d bytes audio/mpeg", round, len(got), mime, ok, len(want))
		}
		dials := dialer.count() - before
		if wantDials := map[int]int{1: 2, 2: 1}[round]; dials != wantDials {
			t.Fatalf("round %d: %d conns dialed, want %d (the invite, plus the tone only when not cached)", round, dials, wantDials)
		}
		if err := mgrB.Decline(inv.CallID); err != nil {
			t.Fatal(err)
		}
		waitDial(t, mgrA, callID, 2*time.Second, "declined", func(d Dial, ok bool) bool { return ok && d.State.Ended() })
		_ = mgrA.Hangup(callID) //nolint:errcheck
	}
}

// TestToneServedOnlyToTheRingingCaller: the tone goes to whoever is calling,
// while they are — not to anyone who knows the key, and not over and over.
func TestToneServedOnlyToTheRingingCaller(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	pkC, _ := cipher.GenerateKeyPair()
	lis := newMemListener()
	defer lis.Close() //nolint:errcheck
	rang := make(chan Sig, 1)
	mgrB := NewManager(Config{LocalPK: pkB, Dial: neverDial, ManualAnswer: true, Ring: func(inv Sig) { rang <- inv }})
	data := []byte("a short tone")
	if err := mgrB.SetRingback(data, "audio/ogg"); err != nil {
		t.Fatal(err)
	}
	hash := toneHash(data)
	go mgrB.Serve(ctx, lis)
	dial := func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return lis.dial() }
	stranger := NewSignaler(pkC, 0, dial, nil)
	caller := NewSignaler(pkA, 0, dial, nil)

	// No call ringing: nothing to fetch.
	if _, err := stranger.FetchTone(ctx, pkB, "nope", hash, len(data)); !errors.Is(err, ErrToneRefused) {
		t.Fatalf("fetch with no call ringing: %v, want ErrToneRefused", err)
	}

	// A call from A rings; the invite is written by hand so A's own
	// Manager does not fetch first.
	c, err := lis.dial()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close() //nolint:errcheck
	if err := writeSig(c, Sig{Type: SigInvite, CallID: "call-1", FromPK: pkA, Progress: true}); err != nil {
		t.Fatal(err)
	}
	awaitRing(t, rang)

	if _, err := stranger.FetchTone(ctx, pkB, "call-1", hash, len(data)); !errors.Is(err, ErrToneRefused) {
		t.Fatalf("fetch by someone other than the caller: %v, want ErrToneRefused", err)
	}
	for i := 0; i < toneFetchesPerCall; i++ {
		got, err := caller.FetchTone(ctx, pkB, "call-1", hash, len(data))
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("fetch %d by the caller: %q, %v", i+1, got, err)
		}
	}
	if _, err := caller.FetchTone(ctx, pkB, "call-1", hash, len(data)); !errors.Is(err, ErrToneRefused) {
		t.Fatalf("fetch past the per-call cap: %v, want ErrToneRefused", err)
	}
	_ = mgrB.Decline("call-1") //nolint:errcheck
}

func TestSetRingbackBounds(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	m := NewManager(Config{LocalPK: pk, Dial: neverDial})
	if err := m.SetRingback(make([]byte, MaxRingbackSize+1), "audio/mpeg"); err == nil {
		t.Fatal("an oversized tone was accepted")
	}
	if err := m.SetRingback([]byte("x"), "text/html"); err == nil {
		t.Fatal("a non-audio type was accepted")
	}
	if err := m.SetRingback([]byte("x"), "audio/webm;codecs=opus"); err != nil {
		t.Fatalf("audio/webm with codecs: %v", err)
	}
	if data, mime := m.Ringback(); string(data) != "x" || mime != "audio/webm" {
		t.Fatalf("Ringback() = %q %q", data, mime)
	}
	if err := m.SetRingback(nil, ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if data, _ := m.Ringback(); data != nil {
		t.Fatal("the tone survived being cleared")
	}
}

func TestNormalizeToneMime(t *testing.T) {
	for in, want := range map[string]string{
		"audio/mpeg":              "audio/mpeg",
		"Audio/MP3":               "audio/mpeg",
		"audio/ogg; codecs=opus":  "audio/ogg",
		"video/webm":              "audio/webm",
		"audio/x-m4a":             "audio/mp4",
		"text/html":               "",
		"image/svg+xml":           "",
		"":                        "",
		"audio/wav;rate=8000;x=y": "audio/wav",
	} {
		if got := NormalizeToneMime(in); got != want {
			t.Errorf("NormalizeToneMime(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOutcomeMapping(t *testing.T) {
	bg := context.Background()
	canceled, cancel := context.WithCancel(bg)
	cancel()
	expired, cancel2 := context.WithDeadline(bg, time.Now().Add(-time.Second))
	defer cancel2()

	if _, _, ok := dialErrOutcome(canceled, DialRinging, errors.New("x")); ok {
		t.Error("a caller's own cancel produced an outcome")
	}
	if s, _, _ := dialErrOutcome(bg, DialConnecting, fmt.Errorf("dial: %w: boom", ErrUnreachable)); s != DialOffline {
		t.Errorf("unreachable → %q, want offline", s)
	}
	if s, _, _ := dialErrOutcome(expired, DialRinging, errors.New("no answer")); s != DialNoAnswer {
		t.Errorf("ring budget spent while ringing → %q, want no_answer", s)
	}
	if s, _, _ := dialErrOutcome(expired, DialConnecting, errors.New("slow")); s != DialOffline {
		t.Errorf("budget spent still connecting → %q, want offline", s)
	}
	if s, _, _ := dialErrOutcome(bg, DialCalling, io.EOF); s != DialFailed {
		t.Errorf("conn dropped → %q, want failed", s)
	}
	for _, tc := range []struct {
		reply Sig
		want  DialState
	}{
		{Sig{Type: SigBusy}, DialBusy},
		{Sig{Type: SigDecline, Reason: "no answer"}, DialNoAnswer},
		{Sig{Type: SigDecline, Reason: "declined"}, DialDeclined},
		{Sig{Type: SigDecline}, DialDeclined},
		{Sig{Type: SigDecline, Reason: "voice not enabled"}, DialFailed},
	} {
		if s, _ := replyOutcome(tc.reply); s != tc.want {
			t.Errorf("replyOutcome(%+v) = %q, want %q", tc.reply, s, tc.want)
		}
	}
}
