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

	// B has a live call with pkPeer.
	const callID = "abcabcabcabcabca"
	near, far := net.Pipe()
	defer far.Close() //nolint:errcheck
	live := mgrB.startSession(callID, pkPeer, near, 1)
	live.SetAwaitResume()
	defer live.Close()

	stranger := NewManager(Config{
		LocalPK: pkStranger,
		Dial:    func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return lis.dial() },
	})
	if _, err := stranger.sig.Resume(ctx, pkB, callID); err == nil {
		t.Fatal("a resume from a stranger was accepted for somebody else's call")
	}

	// The refusal must not have disturbed the call it failed to hijack.
	if got, _ := live.media(); got != near {
		t.Fatal("the stranger's refused resume replaced the call's transport")
	}
	if r := live.EndReason(); r != "" {
		t.Fatalf("the refused resume ended the call: %q", r)
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

// blackholeConn is a transport that has died without saying so: writes are
// swallowed and reads block, which is what a dead TCP connection looks like to
// the side that is mostly writing — the kernel accepts into the send buffer
// and nothing ever comes back.
type blackholeConn struct {
	net.Conn
	dead chan struct{}
	once sync.Once
}

func newBlackholeConn() *blackholeConn {
	near, far := net.Pipe()
	_ = far.Close() //nolint:errcheck // only near is used, for its net.Conn surface
	return &blackholeConn{Conn: near, dead: make(chan struct{})}
}

func (c *blackholeConn) Read([]byte) (int, error)    { <-c.dead; return 0, io.EOF }
func (c *blackholeConn) Write(b []byte) (int, error) { return len(b), nil }
func (c *blackholeConn) Close() error {
	c.once.Do(func() { close(c.dead) })
	return nil
}

// TestAdoptionDoesNotNeedThisSideToHaveNoticed is the fix for what a real soak
// test caught: the two ends of a call do not find out the transport died at
// the same time, and can be the better part of a minute apart.
//
// Measured, on the run that produced this test: the caller noticed at 18:07:57
// and gave up re-dialing at 18:08:27; the callee did not notice until 18:08:51
// — twenty-four seconds after the only peer that was going to call back had
// stopped. While resumption required BOTH sides to be in a reconnecting state
// at once, their windows simply missed each other and the call died anyway.
//
// So adoption does not ask. A session that believes everything is fine takes
// the replacement and carries on with it.
func TestAdoptionDoesNotNeedThisSideToHaveNoticed(t *testing.T) {
	dead := newBlackholeConn()
	sess := NewSession("feedfacefeedface", dead, NewPCMCodec(), &toneSource{}, NullSink{}, 1, nil)
	sess.SetAwaitResume()
	sess.SetPeer(cipher.PubKey{})

	done := make(chan struct{})
	go func() { sess.Run(context.Background()); close(done) }()
	defer func() { sess.Close(); <-done }()

	// Let it settle into "happily writing into nowhere": it has noticed
	// nothing, so there is no reconnect in flight for the adoption to race.
	time.Sleep(100 * time.Millisecond)
	if r := sess.EndReason(); r != "" {
		t.Fatalf("the session ended before the test began: %q", r)
	}

	near, far := net.Pipe()
	defer far.Close() //nolint:errcheck
	if !sess.AdoptConn(near) {
		t.Fatal("a live session refused the peer's replacement transport")
	}

	// Media must now be arriving on the new conn.
	if err := far.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	var hdr [2]byte
	if _, err := io.ReadFull(far, hdr[:]); err != nil {
		t.Fatalf("no media on the adopted transport: %v", err)
	}
	n := int(hdr[0])<<8 | int(hdr[1])
	if n == 0 || n > sigMaxLen {
		t.Fatalf("adopted transport carried a nonsense frame length %d", n)
	}
	if _, err := io.ReadFull(far, make([]byte, n)); err != nil {
		t.Fatalf("frame body truncated on the adopted transport: %v", err)
	}
}
