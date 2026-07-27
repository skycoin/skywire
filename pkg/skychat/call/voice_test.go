// Package call pkg/skychat/call/voice_test.go
package call

import (
	"context"
	"io"
	"math"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestSigCodecRoundTrip(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	in := Sig{Type: SigInvite, CallID: "abc123", FromPK: pk, Codec: "pcm", MediaPort: 63}
	pr, pw := io.Pipe()
	go func() { writeSig(pw, in); pw.Close() }() //nolint:errcheck,gosec
	out, err := readSig(pr)
	if err != nil {
		t.Fatalf("readSig: %v", err)
	}
	if out.Type != in.Type || out.CallID != in.CallID || out.FromPK != in.FromPK ||
		out.Codec != in.Codec || out.MediaPort != in.MediaPort {
		t.Fatalf("round-trip mismatch: %+v != %+v", out, in)
	}
}

func TestPCMCodecRoundTrip(t *testing.T) {
	c := NewPCMCodec()
	pcm := make([]int16, frameSamples)
	for i := range pcm {
		pcm[i] = int16(i - frameSamples/2)
	}
	payload, err := c.Encode(pcm)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := c.Decode(payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != len(pcm) {
		t.Fatalf("len %d != %d", len(got), len(pcm))
	}
	for i := range pcm {
		if got[i] != pcm[i] {
			t.Fatalf("sample %d: %d != %d", i, got[i], pcm[i])
		}
	}
}

// memListener is an in-memory net.Listener: dial() hands the caller one end of a
// net.Pipe and queues the other for Accept.
type memListener struct {
	ch     chan net.Conn
	once   sync.Once
	closed chan struct{}
}

func newMemListener() *memListener {
	return &memListener{ch: make(chan net.Conn), closed: make(chan struct{})}
}
func (l *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, io.EOF
	}
}
func (l *memListener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *memListener) Addr() net.Addr { return pipeAddr{} }
func (l *memListener) dial() (net.Conn, error) {
	a, b := net.Pipe()
	select {
	case l.ch <- a:
		return b, nil
	case <-l.closed:
		return nil, io.ErrClosedPipe
	}
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "mem" }
func (pipeAddr) String() string  { return "mem" }

// toneSource emits a 440 Hz sine so the received audio is verifiably non-silent.
type toneSource struct{ n int }

func (s *toneSource) Read(pcm []int16) (int, error) {
	for i := range pcm {
		t := float64(s.n) / float64(sampleRate)
		pcm[i] = int16(8000 * math.Sin(2*math.Pi*440*t))
		s.n++
	}
	return len(pcm), nil
}

// captureSink records every received frame.
type captureSink struct {
	mu     sync.Mutex
	frames [][]int16
}

func (c *captureSink) Write(pcm []int16) (int, error) {
	cp := make([]int16, len(pcm))
	copy(cp, pcm)
	c.mu.Lock()
	c.frames = append(c.frames, cp)
	c.mu.Unlock()
	return len(pcm), nil
}
func (c *captureSink) count() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.frames) }

// TestCallDeliversAudio runs a full call A->B in memory and asserts B receives
// decoded audio frames — the whole control (invite/accept) + media (RTP over a
// skywire-shaped net.Conn) plane, end to end.
func TestCallDeliversAudio(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	lis := newMemListener()
	defer lis.Close() //nolint:errcheck

	// Callee B: auto-answer, capture what it hears.
	sink := &captureSink{}
	mgrB := NewManager(Config{
		LocalPK:    pkB,
		Dial:       func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return nil, io.EOF },
		OnIncoming: func(Sig) bool { return true },
		NewSink:    func() Sink { return sink },
	})
	go mgrB.Serve(ctx, lis)

	// Caller A: dials B's in-memory listener; speaks a tone.
	mgrA := NewManager(Config{
		LocalPK:   pkA,
		Dial:      func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return lis.dial() },
		NewSource: func() Source { return &toneSource{} },
	})

	sess, err := mgrA.Call(ctx, pkB)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if sess.CallID == "" {
		t.Fatal("empty call id")
	}

	// ~7 frames should arrive within ~200ms (20ms cadence).
	deadline := time.After(1500 * time.Millisecond)
	for sink.count() < 5 {
		select {
		case <-deadline:
			t.Fatalf("only %d frames received", sink.count())
		case <-time.After(10 * time.Millisecond):
		}
	}

	// Frames must be full 20ms PCM frames and not all-zero (the tone).
	sink.mu.Lock()
	f := sink.frames[len(sink.frames)-1]
	sink.mu.Unlock()
	if len(f) != frameSamples {
		t.Fatalf("frame size %d != %d", len(f), frameSamples)
	}
	nonzero := false
	for _, s := range f {
		if s != 0 {
			nonzero = true
			break
		}
	}
	if !nonzero {
		t.Fatal("received frame is all-zero; tone did not round-trip")
	}

	if got := mgrA.Active(); len(got) != 1 {
		t.Fatalf("active calls = %d, want 1", len(got))
	}
	if err := mgrA.Hangup(sess.CallID); err != nil {
		t.Fatalf("hangup: %v", err)
	}
}

// blockingSource mimics a real audio device (or the wasm mic ring) with no
// frames flowing: Read blocks until the source is Closed, then returns EOF.
type blockingSource struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingSource() *blockingSource { return &blockingSource{closed: make(chan struct{})} }
func (s *blockingSource) Read([]int16) (int, error) {
	<-s.closed
	return 0, io.EOF
}
func (s *blockingSource) Close() error { s.once.Do(func() { close(s.closed) }); return nil }

// TestHangupWithBlockedSourceDropsCall is a regression test: when the sendLoop
// is parked inside a blocking Source.Read (e.g. a mic with no frames — the wasm
// case with the audio proxy not started), Hangup must still tear the session
// down and drop it from Active(). Session.Close closes the source so the
// blocked Read unblocks, sendLoop exits, Run's wg.Wait returns, and dropCall
// fires. Without that, the call leaked in the manager forever.
func TestHangupWithBlockedSourceDropsCall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	lis := newMemListener()
	defer lis.Close() //nolint:errcheck

	mgrB := NewManager(Config{
		LocalPK:    pkB,
		Dial:       func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return nil, io.EOF },
		OnIncoming: func(Sig) bool { return true },
	})
	go mgrB.Serve(ctx, lis)

	// Visualize wraps the Source in a teeSource — the wasm case, where the leak
	// actually bit: the tee must forward Close to the blocking inner Source.
	mgrA := NewManager(Config{
		LocalPK:   pkA,
		Dial:      func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return lis.dial() },
		NewSource: func() Source { return newBlockingSource() },
		Visualize: true,
	})
	sess, err := mgrA.Call(ctx, pkB)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got := mgrA.Active(); len(got) != 1 {
		t.Fatalf("active calls = %d, want 1", len(got))
	}
	if err := mgrA.Hangup(sess.CallID); err != nil {
		t.Fatalf("hangup: %v", err)
	}
	// dropCall runs after Run returns; poll for the call to disappear.
	deadline := time.After(2 * time.Second)
	for len(mgrA.Active()) != 0 {
		select {
		case <-deadline:
			t.Fatalf("call not dropped after hangup: %v", mgrA.Active())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestManualAnswerRing exercises the explicit-answer (ring) flow: the callee
// parks the invite until Answer, and the caller's Call returns only then.
func TestManualAnswerRing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	lis := newMemListener()
	defer lis.Close() //nolint:errcheck

	rang := make(chan Sig, 1)
	mgrB := NewManager(Config{
		LocalPK:      pkB,
		Dial:         func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return nil, io.EOF },
		ManualAnswer: true,
		Ring:         func(inv Sig) { rang <- inv },
	})
	go mgrB.Serve(ctx, lis)

	mgrA := NewManager(Config{
		LocalPK: pkA,
		Dial:    func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return lis.dial() },
	})

	type res struct {
		sess *Session
		err  error
	}
	resc := make(chan res, 1)
	go func() { s, e := mgrA.Call(ctx, pkB); resc <- res{s, e} }()

	var inv Sig
	select {
	case inv = <-rang:
	case <-time.After(2 * time.Second):
		t.Fatal("call never rang")
	}
	if inc := mgrB.Incoming(); len(inc) != 1 || inc[0].CallID != inv.CallID {
		t.Fatalf("Incoming() = %v, want the ringing call %s", inc, inv.CallID)
	}
	// Not answered yet → caller still blocked.
	select {
	case r := <-resc:
		t.Fatalf("Call returned before answer: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
	if err := mgrB.Answer(inv.CallID); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	select {
	case r := <-resc:
		if r.err != nil {
			t.Fatalf("Call after answer: %v", r.err)
		}
		if r.sess == nil {
			t.Fatal("nil session after answer")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Call did not return after Answer")
	}
	if inc := mgrB.Incoming(); len(inc) != 0 {
		t.Fatalf("still ringing after answer: %v", inc)
	}
}

// TestManualAnswerDecline: an explicit Decline makes the caller's Call fail.
func TestManualAnswerDecline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	lis := newMemListener()
	defer lis.Close() //nolint:errcheck

	rang := make(chan Sig, 1)
	mgrB := NewManager(Config{
		LocalPK:      pkB,
		Dial:         func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return nil, io.EOF },
		ManualAnswer: true,
		Ring:         func(inv Sig) { rang <- inv },
	})
	go mgrB.Serve(ctx, lis)

	mgrA := NewManager(Config{
		LocalPK: pkA,
		Dial:    func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return lis.dial() },
	})

	errc := make(chan error, 1)
	go func() { _, e := mgrA.Call(ctx, pkB); errc <- e }()

	var inv Sig
	select {
	case inv = <-rang:
	case <-time.After(2 * time.Second):
		t.Fatal("call never rang")
	}
	if err := mgrB.Decline(inv.CallID); err != nil {
		t.Fatalf("Decline: %v", err)
	}
	select {
	case e := <-errc:
		if e == nil {
			t.Fatal("Call should fail after decline")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Call did not return after Decline")
	}
}

// TestCallAudioTap verifies Visualize mode taps a call's sent audio so
// CallAudio can serve it for the live spectrogram.
func TestCallAudioTap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	lis := newMemListener()
	defer lis.Close() //nolint:errcheck

	mgrB := NewManager(Config{
		LocalPK:    pkB,
		Dial:       func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return nil, io.EOF },
		OnIncoming: func(Sig) bool { return true },
	})
	go mgrB.Serve(ctx, lis)

	mgrA := NewManager(Config{
		LocalPK:   pkA,
		Dial:      func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return lis.dial() },
		NewSource: func() Source { return &toneSource{} },
		Visualize: true,
	})
	sess, err := mgrA.Call(ctx, pkB)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	// Let a few frames of the tone flow through the tap.
	time.Sleep(300 * time.Millisecond)
	sent, _, err := mgrA.CallAudio(sess.CallID)
	if err != nil {
		t.Fatalf("CallAudio: %v", err)
	}
	if len(sent) == 0 {
		t.Fatal("tap captured no sent audio")
	}
	nonzero := false
	for _, s := range sent {
		if s != 0 {
			nonzero = true
			break
		}
	}
	if !nonzero {
		t.Fatal("tapped sent audio is all silence")
	}
}
