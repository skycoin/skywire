// Package call pkg/skychat/call/bridge.go c4-app-chat
//
// Bridge is a voice audio device that lives OUTSIDE the visor process.
//
// Every other backend here opens a device the visor can reach for itself:
// PulseAudio on Linux, miniaudio on Windows/macOS, WebAudio in the browser.
// **Android has none of them.** GOOS=android satisfies the `linux` build tag,
// so the visor there compiles the PulseAudio backend, finds no server to talk
// to, and degrades to SilentSource + NullSink — a call that connects and is
// mute in both directions. The device that does exist belongs to the host app
// (AudioRecord and AudioTrack, in the Android process that started the visor),
// so the visor borrows it: the app pushes captured PCM in and pulls playback
// PCM out over the visor's local API, and this is the seam in the middle.
//
// Nothing here is Android-specific — it is "the audio device is someone else's
// problem", which is also what a headless host with a remote operator wants.
//
// **Why a fan-out and not one buffer each way.** Source and Sink are built PER
// CALL SESSION, and there is exactly one device. Capture is therefore COPIED to
// every session — each gets its own ring, so one session's read cannot swallow
// a frame another session was going to get — and playback is SUMMED across
// sessions with clipping, which is what a real mixer does when two calls share
// one speaker. With the single call a phone actually places, both collapse to
// the obvious thing.
package call

import (
	"math"
	"sync"
)

// The format every byte crossing the bridge is in: mono int16 at [SampleRate],
// in frames of [FrameSamples]. Exported because the host app has to configure
// its capture and playback to match, and a mismatch is silent corruption
// (wrong pitch, not an error).
const (
	SampleRate   = sampleRate
	FrameSamples = frameSamples
)

// bridgeBufferSamples caps each per-session ring at ~1 s. Drop-oldest beyond
// that: in a live call, audio that is a second late is worse than no audio,
// and an unbounded buffer would turn a stalled reader into growing latency
// that never recovers.
const bridgeBufferSamples = sampleRate

// Bridge is the host app's microphone and speaker, presented to the call
// manager as ordinary Source/Sink factories. The zero value is not usable —
// call NewBridge.
type Bridge struct {
	mu   sync.Mutex
	mics map[*sampleRing]struct{}
	spks map[*sampleRing]struct{}
}

// NewBridge returns an idle bridge. It is safe to use before the host app has
// ever connected: capture then reads silence and playback is discarded, which
// is exactly how a machine with no audio hardware behaves.
func NewBridge() *Bridge {
	return &Bridge{
		mics: make(map[*sampleRing]struct{}),
		spks: make(map[*sampleRing]struct{}),
	}
}

// Source returns a capture handle for one call session. Closing it (the
// session does, via io.Closer) unregisters it, so a finished call stops
// receiving copies of the microphone.
func (b *Bridge) Source() Source {
	r := newSampleRing(bridgeBufferSamples)
	b.mu.Lock()
	b.mics[r] = struct{}{}
	b.mu.Unlock()
	return &bridgeSource{bridge: b, ring: r}
}

// Sink returns a playback handle for one call session.
func (b *Bridge) Sink() Sink {
	r := newSampleRing(bridgeBufferSamples)
	b.mu.Lock()
	b.spks[r] = struct{}{}
	b.mu.Unlock()
	return &bridgeSink{bridge: b, ring: r}
}

// Push hands captured PCM to every live call. Never blocks: a call whose ring
// is full drops its oldest audio rather than stalling the capture thread.
func (b *Bridge) Push(pcm []int16) {
	if len(pcm) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for r := range b.mics {
		r.push(pcm)
	}
}

// Pull fills dst with what the calls want played, mixed, padding with silence.
// Always fills dst completely and returns len(dst) — the host app's playback
// wants a frame on every tick, silence included, or its own buffer underruns
// and clicks.
func (b *Bridge) Pull(dst []int16) int {
	for i := range dst {
		dst[i] = 0
	}
	if len(dst) == 0 {
		return 0
	}

	// Snapshot under the lock, drain outside it: draining takes each ring's
	// own lock, and holding both at once would order two locks for no reason.
	b.mu.Lock()
	rings := make([]*sampleRing, 0, len(b.spks))
	for r := range b.spks {
		rings = append(rings, r)
	}
	b.mu.Unlock()

	switch len(rings) {
	case 0:
		return len(dst)
	case 1:
		return rings[0].popSilence(dst)
	}

	frame := make([]int16, len(dst))
	for _, r := range rings {
		r.popSilence(frame)
		for i, s := range frame {
			dst[i] = clipSample(int32(dst[i]) + int32(s))
		}
	}
	return len(dst)
}

// Live reports whether any call currently holds the device. The host app uses
// it to decide whether to run capture and playback at all — an idle bridge
// should not be holding the microphone open.
func (b *Bridge) Live() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.mics) > 0 || len(b.spks) > 0
}

// clipSample saturates a mixed sample instead of letting it wrap, which would
// turn a loud moment into a burst of noise.
func clipSample(v int32) int16 {
	switch {
	case v > math.MaxInt16:
		return math.MaxInt16
	case v < math.MinInt16:
		return math.MinInt16
	default:
		return int16(v)
	}
}

// bridgeSource is one call's view of the host microphone.
type bridgeSource struct {
	bridge *Bridge
	ring   *sampleRing
}

// Read never blocks: it returns whatever capture has delivered and pads with
// silence, so the send loop keeps emitting a frame per tick even while the app
// is not pushing. A blocking read here would starve the peer into the dmsg
// idle-read timeout and drop the call.
func (s *bridgeSource) Read(pcm []int16) (int, error) { return s.ring.popSilence(pcm), nil }

// Close unregisters this call from the microphone fan-out.
func (s *bridgeSource) Close() error {
	s.bridge.mu.Lock()
	delete(s.bridge.mics, s.ring)
	s.bridge.mu.Unlock()
	s.ring.close()
	return nil
}

// bridgeSink is one call's view of the host speaker.
type bridgeSink struct {
	bridge *Bridge
	ring   *sampleRing
}

func (s *bridgeSink) Write(pcm []int16) (int, error) {
	s.ring.push(pcm)
	return len(pcm), nil
}

// Close unregisters this call from the playback mix.
func (s *bridgeSink) Close() error {
	s.bridge.mu.Lock()
	delete(s.bridge.spks, s.ring)
	s.bridge.mu.Unlock()
	s.ring.close()
	return nil
}
