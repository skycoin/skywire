// Package router pkg/router/leg_rtt_window.go c2-net-routing
package router

import "time"

// Load-robust per-leg latency statistic for the latency-band controller.
//
// The band judged a leg on its LATEST end-to-end latency sample. That sample is
// taken by an in-band liveness pong which shares the leg's FIFO send queue with
// bulk data, so under load it reads base_RTT + self-inflicted queuing delay and
// swings wildly: one leg measured live 2026-09-16 walked 37 → 136 → 771 → 955 →
// 435 → 346 ms within a single download. Judged sample-by-sample the leg falls
// out of band and back in on alternate ticks, and with the 30s minimum park hold
// (#4968) that became a park/promote every 30s for the whole transfer.
//
// The minimum over a sliding window is the standard escape (it is what TCP's
// min-RTT / BBR's RTprop estimate does): queuing delay only ever ADDS to the
// path's propagation delay, so the smallest sample in the recent past is the
// closest estimate of the leg's real latency that load cannot inflate. A leg
// that is genuinely slow has no small samples and is still parked; a leg that is
// merely busy has them and is kept.
const (
	// legRTTMinWindow is how far back the min-RTT statistic looks. It matches
	// legParkMinHold, so a leg must look bad for at least as long as a park
	// lasts before one is taken — which is exactly the flap the window ends.
	legRTTMinWindow = 30 * time.Second
	// legRTTMinSamplesCap bounds the retained samples per leg. The liveness
	// pong is the producer and runs far slower than this, but a leg fed by a
	// faster sampler must not grow without limit.
	legRTTMinSamplesCap = 64
)

// legRTTSample is one raw end-to-end round-trip sample with its arrival time.
type legRTTSample struct {
	ms float64
	at time.Time
}

// legRTTWindow is one leg's sliding window of raw end-to-end RTT samples, kept
// in arrival order. Not safe for concurrent use; the RouteGroup serializes
// pushes and reads under legLivenessMu (the same lock guarding legE2ELatency
// and legOWD).
type legRTTWindow struct {
	s []legRTTSample
}

// push records one sample (ms) and evicts everything older than legRTTMinWindow.
// Non-positive samples are ignored.
func (w *legRTTWindow) push(sampleMs float64, now time.Time) {
	if w == nil || sampleMs <= 0 {
		return
	}
	w.s = append(w.s, legRTTSample{ms: sampleMs, at: now})
	w.evict(now)
}

// evict drops samples that fell out of the window and caps the retained count.
func (w *legRTTWindow) evict(now time.Time) {
	cutoff := now.Add(-legRTTMinWindow)
	i := 0
	for i < len(w.s) && w.s[i].at.Before(cutoff) {
		i++
	}
	if i > 0 {
		w.s = append(w.s[:0], w.s[i:]...)
	}
	if over := len(w.s) - legRTTMinSamplesCap; over > 0 {
		w.s = append(w.s[:0], w.s[over:]...)
	}
}

// minMs returns the smallest sample still inside the window, or 0 when the
// window holds none. Samples are NOT evicted here so the read stays usable from
// a read path; stale entries only ever make the minimum smaller, i.e. more
// conservative about parking, and the next push clears them.
func (w *legRTTWindow) minMs(now time.Time) float64 {
	if w == nil {
		return 0
	}
	cutoff := now.Add(-legRTTMinWindow)
	best := 0.0
	for _, s := range w.s {
		if s.at.Before(cutoff) {
			continue
		}
		if best == 0 || s.ms < best {
			best = s.ms
		}
	}
	return best
}
