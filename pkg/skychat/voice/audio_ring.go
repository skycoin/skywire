//go:build linux || (voiceaudio && (windows || darwin)) || (js && wasm)

// Package voice pkg/skychat/voice/audio_ring.go c2-app-chat
//
// sampleRing is the bounded int16 FIFO shared by all audio backends: the pure-Go
// PulseAudio backend on Linux, the cgo malgo backend on Windows/macOS, and the
// browser WebAudio backend on js/wasm. It bridges a push-model capture callback
// and a pull-model playback callback to the voice package's pull-model Source /
// push-model Sink.
package voice

import "sync"

// sampleRing keeps the most recent int16 samples (drop-oldest on overflow so
// latency can't grow unbounded).
//
// popBlocking (capture) waits for a full frame so a reader is naturally paced by
// the capture rate rather than spinning and draining silence. popSilence
// (playback) never blocks — the device needs samples immediately and gets
// silence on underrun.
type sampleRing struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []int16
	max    int
	closed bool
}

func newSampleRing(max int) *sampleRing {
	r := &sampleRing{max: max}
	r.cond = sync.NewCond(&r.mu)
	return r
}

func (r *sampleRing) push(s []int16) {
	r.mu.Lock()
	r.buf = append(r.buf, s...)
	if len(r.buf) > r.max {
		r.buf = append(r.buf[:0], r.buf[len(r.buf)-r.max:]...)
	}
	r.cond.Broadcast()
	r.mu.Unlock()
}

// popBlocking fills dst, waiting until a full frame is buffered (or the ring is
// closed, after which it pads silence and returns).
func (r *sampleRing) popBlocking(dst []int16) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(r.buf) < len(dst) && !r.closed {
		r.cond.Wait()
	}
	return r.drainLocked(dst)
}

// popSilence fills dst with whatever is buffered, padding the tail with silence,
// never blocking.
func (r *sampleRing) popSilence(dst []int16) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.drainLocked(dst)
}

func (r *sampleRing) drainLocked(dst []int16) int {
	n := copy(dst, r.buf)
	r.buf = append(r.buf[:0], r.buf[n:]...)
	for i := n; i < len(dst); i++ {
		dst[i] = 0
	}
	return len(dst)
}

func (r *sampleRing) close() {
	r.mu.Lock()
	r.closed = true
	r.cond.Broadcast()
	r.mu.Unlock()
}
