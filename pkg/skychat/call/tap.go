// Package call pkg/skychat/call/tap.go c4-app-chat
//
// Per-call audio taps for visualization. When a Manager has Visualize set, each
// session's Source (sent audio) and Sink (received audio) are wrapped so a copy
// of every frame lands in a small bounded ring. CallAudio(callID) returns the
// most recent ~1s of each direction, which the CLI polls to draw a two-panel
// sent/received spectrogram during a call. The audio itself is untouched — the
// tee just observes.
package call

import (
	"io"
	"sync"
)

// audioRing keeps the most recent int16 samples (drop-oldest on overflow).
type audioRing struct {
	mu  sync.Mutex
	buf []int16
	max int
}

func newAudioRing(max int) *audioRing { return &audioRing{max: max} }

func (r *audioRing) add(s []int16) {
	r.mu.Lock()
	r.buf = append(r.buf, s...)
	if len(r.buf) > r.max {
		r.buf = append(r.buf[:0], r.buf[len(r.buf)-r.max:]...)
	}
	r.mu.Unlock()
}

func (r *audioRing) snapshot() []int16 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int16, len(r.buf))
	copy(out, r.buf)
	return out
}

// teeSource wraps a Source, copying each read frame into a ring (the SENT audio).
type teeSource struct {
	inner Source
	ring  *audioRing
}

func (t *teeSource) Read(pcm []int16) (int, error) {
	n, err := t.inner.Read(pcm)
	if n > 0 {
		t.ring.add(pcm[:n])
	}
	return n, err
}

// Close forwards to the wrapped Source so Session.Close can unblock a blocking
// Read (e.g. an audio ring). Without this the tee hides the inner Closer and a
// wedged sendLoop would never let the call be dropped.
func (t *teeSource) Close() error {
	if c, ok := t.inner.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// teeSink wraps a Sink, copying each written frame into a ring (RECEIVED audio).
type teeSink struct {
	inner Sink
	ring  *audioRing
}

func (t *teeSink) Write(pcm []int16) (int, error) {
	t.ring.add(pcm)
	return t.inner.Write(pcm)
}

// Close forwards to the wrapped Sink (mirrors teeSource.Close).
func (t *teeSink) Close() error {
	if c, ok := t.inner.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// callTap holds the sent/received audio rings for one active call.
type callTap struct {
	sent *audioRing
	recv *audioRing
}
