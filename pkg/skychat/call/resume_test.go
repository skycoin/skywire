// Package call pkg/skychat/call/resume_test.go
package call

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// recordingDialer hands out conns through lis and keeps them, so a test can
// sever the one a call is using the way a dying dmsg session would: the socket
// goes, without either endpoint deciding to hang up.
type recordingDialer struct {
	lis *memListener
	mu  sync.Mutex
	out []net.Conn
}

func (d *recordingDialer) dial(context.Context, cipher.PubKey, uint16) (net.Conn, error) {
	c, err := d.lis.dial()
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.out = append(d.out, c)
	d.mu.Unlock()
	return c, nil
}

func (d *recordingDialer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.out)
}

// breakNth severs the n-th conn handed out (0-based).
func (d *recordingDialer) breakNth(n int) {
	d.mu.Lock()
	c := d.out[n]
	d.mu.Unlock()
	_ = c.Close() //nolint:errcheck
}

// TestCallSurvivesTransportLoss is the point of call resumption: a call's
// media rides ONE stream on a SHARED dmsg session, so anything that takes the
// session down — a server evicting us, a reaper, a phone moving from Wi-Fi to
// cellular — took the call with it. The call now rebuilds its transport and
// keeps going.
func TestCallSurvivesTransportLoss(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	lis := newMemListener()
	defer lis.Close() //nolint:errcheck
	dialer := &recordingDialer{lis: lis}

	sink := &captureSink{}
	mgrB := NewManager(Config{
		LocalPK:    pkB,
		Dial:       func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return nil, io.EOF },
		OnIncoming: func(Sig) bool { return true },
		NewSink:    func() Sink { return sink },
	})
	go mgrB.Serve(ctx, lis)

	mgrA := NewManager(Config{
		LocalPK:   pkA,
		Dial:      dialer.dial,
		NewSource: func() Source { return &toneSource{} },
	})

	sess, err := mgrA.Call(ctx, pkB)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	waitFrames(t, sink, 5, 3*time.Second, "before the break")

	// Sever the transport. Neither side hung up; the socket simply went.
	beforeBreak := sink.count()
	dialer.breakNth(0)

	// Audio must resume on a rebuilt conn. Generous, because the caller
	// retries on a fixed interval and its first attempt usually lands before
	// the callee has even noticed it needs one.
	waitFrames(t, sink, beforeBreak+25, 25*time.Second, "after the break")

	if got := dialer.count(); got < 2 {
		t.Fatalf("the call was never re-dialed (conns handed out: %d)", got)
	}
	if got := mgrA.Active(); len(got) != 1 || got[0] != sess.CallID {
		t.Fatalf("caller's active calls = %v, want just %s", got, sess.CallID)
	}
	if got := mgrB.Active(); len(got) != 1 {
		t.Fatalf("callee's active calls = %v, want exactly one", got)
	}
	if r := sess.EndReason(); r != "" {
		t.Fatalf("a call that recovered reported an end reason: %q", r)
	}
}

// waitFrames blocks until the sink has at least n frames.
func waitFrames(t *testing.T, sink *captureSink, n int, within time.Duration, when string) {
	t.Helper()
	deadline := time.After(within)
	for sink.count() < n {
		select {
		case <-deadline:
			t.Fatalf("%s: only %d frames, want %d", when, sink.count(), n)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// TestResumeRefusedForUnknownCall: a resume attaches to a live call without
// ringing anyone, so it must attach to nothing else. An id naming no call
// awaiting a transport is refused.
func TestResumeRefusedForUnknownCall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	lis := newMemListener()
	defer lis.Close() //nolint:errcheck

	mgrB := NewManager(Config{
		LocalPK:      pkB,
		Dial:         func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return nil, io.EOF },
		ManualAnswer: true,
	})
	go mgrB.Serve(ctx, lis)

	mgrA := NewManager(Config{
		LocalPK: pkA,
		Dial:    func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return lis.dial() },
	})

	if _, err := mgrA.sig.Resume(ctx, pkB, "0000000000000000"); err == nil {
		t.Fatal("a resume for a call that does not exist was accepted")
	}
}

// TestResumeRefusedFromWrongPeer: the id alone must not be enough. A resume
// claiming a call that is genuinely awaiting one, but arriving from somebody
// else, is refused — otherwise whoever learned an id could join the audio.
func TestResumeRefusedFromWrongPeer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pkB, _ := cipher.GenerateKeyPair()
	pkPeer, _ := cipher.GenerateKeyPair()
	pkStranger, _ := cipher.GenerateKeyPair()

	lis := newMemListener()
	defer lis.Close() //nolint:errcheck

	mgrB := NewManager(Config{
		LocalPK: pkB,
		Dial:    func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return nil, io.EOF },
	})
	go mgrB.Serve(ctx, lis)

	// B is waiting for pkPeer to come back for call "abc".
	const callID = "abcabcabcabcabca"
	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		_, _ = mgrB.resumeInbound(ctx, callID, pkPeer) //nolint:errcheck
	}()
	waitFor(t, func() bool {
		mgrB.mu.Lock()
		defer mgrB.mu.Unlock()
		return mgrB.resuming[callID] != nil
	}, 2*time.Second, "B never registered the resume wait")

	stranger := NewManager(Config{
		LocalPK: pkStranger,
		Dial:    func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return lis.dial() },
	})
	if _, err := stranger.sig.Resume(ctx, pkB, callID); err == nil {
		t.Fatal("a resume from a stranger was accepted for somebody else's call")
	}

	// And the real peer's call is still waiting, not consumed by the attempt.
	mgrB.mu.Lock()
	still := mgrB.resuming[callID] != nil
	mgrB.mu.Unlock()
	if !still {
		t.Fatal("the stranger's refused resume canceled the genuine wait")
	}
	cancel()
	<-waitDone
}

func waitFor(t *testing.T, cond func() bool, within time.Duration, msg string) {
	t.Helper()
	deadline := time.After(within)
	for !cond() {
		select {
		case <-deadline:
			t.Fatal(msg)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestHangupDuringReconnectEndsTheCallNow: the red button is the one control
// whose whole point is that it takes effect immediately, and a reconnect in
// flight must not hold it up.
//
// Without this the hang-up was honored only when the reconnect budget expired
// — half a minute of a call the user had already ended still reported as live.
func TestHangupDuringReconnectEndsTheCallNow(t *testing.T) {
	near, far := net.Pipe()
	sess := NewSession("deadbeefdeadbeef", near, NewPCMCodec(), SilentSource{}, NullSink{}, 1, nil)

	var once sync.Once
	started := make(chan struct{})
	sess.SetResume(func(ctx context.Context, _ string) (net.Conn, error) {
		once.Do(func() { close(started) })
		<-ctx.Done() // a reconnect that would otherwise run the full budget
		return nil, ctx.Err()
	})

	done := make(chan struct{})
	go func() { sess.Run(context.Background()); close(done) }()

	_ = far.Close() //nolint:errcheck // the transport dies under the call
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("the session never tried to reconnect")
	}

	sess.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("hanging up during a reconnect did not end the call")
	}
}
