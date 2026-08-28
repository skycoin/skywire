// Package router pkg/router/fec_flush.go — idle-flush policy for the packet-mux
// FEC tail (fec_mux.go, #4270).
//
// THE WEAKNESS THIS CLOSES. Fixed-block FEC only emits repair when a block fills
// to K data frames (fecStriper.Add). On a unidirectional bulk stream the LAST
// block is almost always partial: after the final j<K frames the sender goes
// idle, so those j frames get NO repair, and a tail frame striped onto a slow leg
// stalls the no-skip reorder frontier with no FEC rescue — the one documented
// failure mode of fixed-block FEC on a bulk transfer.
//
// THE FIX (adapted to a systematic block code with ZERO wire change). When the
// sender has been idle for T with a partial block pending, the route-group send
// loop (RouteGroup.fecFlushServiceFn) completes the block by emitting the K-j
// remaining slots as REAL empty PADDING frames through the normal send path. Each
// advances writeSeq and the striper, and the K-th completes the block so Add
// emits the repair as at any block boundary. The padding frames are real sealed
// frames (encoder/decoder agree in the wire domain), they fill the seq gap the
// no-skip reorder would otherwise stall on, and the receiver delivers them as
// 0-byte reads that the mux delivery loop filters out of the app stream (the app
// never writes an empty frame — Write rejects len==0). So the tail block becomes
// a genuine K-full MDS block with no new frame type and no receiver change.
//
// This file holds the pure, deterministic policy the send loop consults; the
// emission itself lives in route_group.go (which owns leg selection).
package router

import "time"

// fecDefaultIdleFlush is the inactivity gap after which a pending partial block is
// flushed. It must be well above normal inter-frame spacing so a merely bursty-
// but-active stream is not flushed mid-block (Add completes that block naturally);
// 50ms comfortably exceeds per-frame spacing at any aggregated rate while bounding
// tail-stall risk to a single idle interval.
const fecDefaultIdleFlush = 50 * time.Millisecond

// hasPartialBlock reports whether a block is currently mid-fill (1..K-1 slots),
// i.e. whether there is a tail for the flush to protect. Cheap; safe to poll.
func (s *fecStriper) hasPartialBlock() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.filled > 0 && s.filled < s.k
}

// fecShouldFlush is the pure timer-flush decision the send loop consults. It is
// deliberately free of time.Now(): the caller passes both the current time and
// the timestamp of the last send, so the policy is fully deterministic and
// unit-testable. It returns true when a partial block is pending AND the stream
// has been idle for at least idle since the last frame. idle<=0 disables flushing.
func fecShouldFlush(now, lastAdd time.Time, idle time.Duration, pending bool) bool {
	if !pending || idle <= 0 {
		return false
	}
	return now.Sub(lastAdd) >= idle
}
