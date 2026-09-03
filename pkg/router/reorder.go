// Package router pkg/router/reorder.go c2-net-routing
package router

import (
	"sync"
	"time"
)

// reorderTimeout is how long a frontier gap may stay open before it counts as a
// stall. It does NOT release the gap: see Insert, which never skips a missing
// sequence, because the mux carries a stateful-AEAD noise stream and delivering
// past a hole desyncs the cipher permanently. It drives the diagnostics and
// leg-prune paths, and the timer-based SACK (RouteGroup.reorderStallServiceFn)
// that asks the sender to retransmit the stuck sequence on a live leg, filling
// the gap IN ORDER. Comfortably larger than any realistic inter-leg latency skew,
// so ordinary reordering never reads as a stall. A var (not const) so tests can
// shrink it.
var reorderTimeout = 1500 * time.Millisecond

// reorderBuffer holds out-of-order packets and delivers them in sequence order.
// When mux mode distributes packets across multiple transports, they may arrive
// out of order. This buffer re-sequences them before passing to the noise layer.
type reorderBuffer struct {
	mu      sync.Mutex
	nextSeq uint32            // next expected sequence number
	buf     map[uint32][]byte // out-of-order packets: seq -> payload
	// gapSince is when the current frontier gap opened (buffer went non-empty
	// while waiting for nextSeq). Zero when the buffer is empty / fully caught
	// up. Read by the stall paths that reorderTimeout feeds; it never releases
	// the gap itself.
	gapSince time.Time
	// maxGap is the emergency cap: the most out-of-order packets held before
	// further ones are DROPPED. Dropping the arrival, not skipping the gap, is
	// the whole point — skipping would corrupt the noise stream (see Insert),
	// whereas a dropped seq is simply re-requested by SACK and retransmitted.
	// Because the leg transports are reliable/ordered, a gap is latency skew
	// (the missing seq is in flight and will arrive), so this cap is sized (see
	// reorderWindow) to the aggregate BDP and is only reached on a genuine
	// mid-stream leg death.
	maxGap int
}

func newReorderBuffer(maxGap int) *reorderBuffer {
	return &reorderBuffer{
		buf:    make(map[uint32][]byte),
		maxGap: maxGap,
	}
}

// Insert adds a packet with the given sequence number. Returns payloads ready
// for in-order delivery. Common case (in-order arrival) requires no allocation.
func (rb *reorderBuffer) Insert(seq uint32, data []byte) [][]byte {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Duplicate or late packet — discard
	if seq < rb.nextSeq {
		return nil
	}

	// Buffer out-of-order packet
	if seq != rb.nextSeq {
		// Emergency OOM backstop: if the buffer is already at capacity, DROP this
		// out-of-order packet rather than skip the gap (delivering past the missing
		// seq corrupts the reliable stream — the bad-record-mac failure). The buffer
		// stays bounded at maxGap; the dropped seq is re-requested by SACK and
		// retransmitted, and the real recovery is the leg-dataprogress prune removing
		// the dead leg so its seqs retransmit on a live one and the frontier drains
		// IN ORDER. If the gap never fills, keepalive/liveness closes the group
		// cleanly — a stall, never corruption. maxGap (reorderWindow) is sized to the
		// aggregate BDP so this is only reached on a genuine mid-stream leg death.
		if len(rb.buf) >= rb.maxGap {
			return nil
		}
		// Make a copy since the underlying packet buffer may be reused
		cp := make([]byte, len(data))
		copy(cp, data)
		rb.buf[seq] = cp

		// Mark when this frontier gap opened, so a stalled leg can be timed for
		// diagnostics / future fast-prune (see below).
		if rb.gapSince.IsZero() {
			rb.gapSince = time.Now()
		}

		// NO time-based skip. The mux carries the route group's NOISE-encrypted
		// byte stream (router_serve.go wraps the RouteGroup with network.EncryptConn
		// before the app sees it), and noise is a stateful AEAD: delivering PAST a
		// missing sequence permanently desyncs the cipher — every later frame then
		// fails its MAC and yields zero plaintext, turning a transient stall into a
		// PERMANENT 0-byte wedge. The old time-based flushAll() "released" the gap
		// to keep the stream "moving (lossy)", but on a noise stream lossy == dead:
		// it manufactured the exact hard stall it meant to avoid, which is why an
		// N-leg mux with one black-holing (e.g. webrtc-under-load) leg carried ~0
		// goodput. Instead HOLD the gap: the receiver's SACK keeps reporting the
		// missing seq and the sender retransmits it CONTIGUOUSLY from its retx
		// buffer over a live leg, so the buffer drains in order with the cipher
		// intact. A leg that stays dead is removed by leg-liveness pruning, after
		// which its unacked seqs are retransmitted on the survivors. reorderTimeout
		// is retained as the stall threshold for the diagnostics/prune path and to
		// drive a timer-based SACK (RouteGroup.reorderStallServiceFn) that asks the
		// sender to RETRANSMIT the stuck sequence on a live leg — filling the gap IN
		// ORDER. There is deliberately NO time-based SKIP here: the RouteGroup is a
		// reliable, ordered net.Conn (a TCP/TLS stream rides it), so delivering PAST
		// a missing sequence leaves a HOLE in the byte stream and the upper protocol
		// dies ("bad record mac"). Per-frame noise makes each FRAME independently
		// decryptable, but the reassembled STREAM must still be gapless — so a gap is
		// only ever closed by the missing seq actually arriving (skew or retransmit),
		// never by skipping it.

		return nil
	}

	// In-order: deliver this packet and any consecutive buffered packets
	var delivered [][]byte
	delivered = append(delivered, data)
	rb.nextSeq = seq + 1

	for {
		next, ok := rb.buf[rb.nextSeq]
		if !ok {
			break
		}
		delivered = append(delivered, next)
		delete(rb.buf, rb.nextSeq)
		rb.nextSeq++
	}

	// Frontier advanced. If nothing remains buffered the gap is fully closed;
	// otherwise a new frontier gap (the next missing seq) is now open, so time
	// it from here.
	if len(rb.buf) == 0 {
		rb.gapSince = time.Time{}
	} else {
		rb.gapSince = time.Now()
	}

	return delivered
}

// GapAge reports how long the current frontier gap has been open, or 0 if the
// buffer is contiguous (no gap). The route group's fast data-progress prune uses
// it to tell a genuinely stuck receiver (a gap held while SACK retransmits) from
// ordinary latency-skew interleave.
func (rb *reorderBuffer) GapAge() time.Duration {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if rb.gapSince.IsZero() {
		return 0
	}
	return time.Since(rb.gapSince)
}

// Pending returns the number of packets currently buffered out-of-order.
func (rb *reorderBuffer) Pending() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return len(rb.buf)
}

// NextSeq returns the next expected sequence number (first not yet delivered in order).
func (rb *reorderBuffer) NextSeq() uint32 {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.nextSeq
}
