// Package router pkg/router/reorder.go c2-net-routing
package router

import (
	"sort"
	"sync"
	"time"
)

// reorderTimeout bounds how long the reorder buffer will hold a frontier gap
// open before releasing it (delivering past the missing sequence). It is the
// time-based companion to maxGap: maxGap caps memory, reorderTimeout caps
// head-of-line latency. With reliable leg transports a genuine gap only occurs
// when a leg dies mid-stream and the SACK retransmit has not yet refilled it;
// rather than stall the stream until liveness prunes that leg, release the gap
// after this long so the flow degrades to lossy-but-moving (never a hard 0-byte
// stall — the graceful-degradation contract for mux>1). Comfortably larger than
// any realistic inter-leg latency skew so normal reordering is never released
// early. A var (not const) so tests can shrink it.
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
	// up. Used to release a gap that has stayed open longer than reorderTimeout.
	gapSince time.Time
	// maxGap is the emergency cap: the max out-of-order packets held before a
	// last-resort force-flush that skips the missing sequence. Because the leg
	// transports are reliable/ordered, a gap is latency skew (the missing seq
	// is in flight and will arrive), so this cap is sized (see reorderWindow)
	// to never trip in normal mux operation — hitting it means a leg has died
	// and stopped delivering, and flushing (lossy) is preferable to stalling
	// the stream forever while liveness prunes that leg.
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
		// is retained as the stall threshold for that diagnostics/prune path.
		_ = reorderTimeout

		// Emergency OOM backstop only: with reliable legs the missing seq arrives
		// (via skew or SACK retransmit) and drains the buffer, so this never trips
		// in normal operation. Reaching it means a leg has gone dead mid-stream AND
		// retransmit has not refilled within maxGap packets. Force-flushing here
		// still skips the gap (and so corrupts a noise stream) — it is a
		// last-resort guard against unbounded memory, not a recovery path; the
		// route group's liveness prune + teardown is the real handler. Do NOT lower
		// maxGap to "save memory": that reintroduces the mux>1 corruption bug.
		if len(rb.buf) >= rb.maxGap {
			return rb.flushAll()
		}
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

// flushAll delivers all buffered packets in sequence order and resets.
// Called when the gap exceeds maxGap to prevent unbounded memory growth.
// The caller must hold rb.mu.
func (rb *reorderBuffer) flushAll() [][]byte {
	if len(rb.buf) == 0 {
		return nil
	}

	seqs := make([]uint32, 0, len(rb.buf))
	for seq := range rb.buf {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })

	delivered := make([][]byte, 0, len(seqs))
	for _, seq := range seqs {
		delivered = append(delivered, rb.buf[seq])
		delete(rb.buf, seq)
	}

	// Advance nextSeq past all delivered
	if len(seqs) > 0 {
		rb.nextSeq = seqs[len(seqs)-1] + 1
	}

	// Buffer drained — the gap is closed (skipped).
	rb.gapSince = time.Time{}

	return delivered
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
