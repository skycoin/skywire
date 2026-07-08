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
// latency can't grow unbounded). All reads go through popSilence: it never
// blocks, returning buffered samples and padding the tail with silence on
// underrun. That keeps the ticker-paced send loop emitting a frame every tick —
// real audio when present, silence when idle — so a quiet side never starves the
// peer into the dmsg idle-read timeout (which dropped calls). A blocking read
// here was the culprit.
type sampleRing struct {
	mu  sync.Mutex
	buf []int16
	max int
}

func newSampleRing(max int) *sampleRing { return &sampleRing{max: max} }

func (r *sampleRing) push(s []int16) {
	r.mu.Lock()
	r.buf = append(r.buf, s...)
	if len(r.buf) > r.max {
		r.buf = append(r.buf[:0], r.buf[len(r.buf)-r.max:]...)
	}
	r.mu.Unlock()
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

// close releases the buffer; a source's Read simply returns silence afterward.
func (r *sampleRing) close() {
	r.mu.Lock()
	r.buf = nil
	r.mu.Unlock()
}
