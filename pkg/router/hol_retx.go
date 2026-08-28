// Package router pkg/router/hol_retx.go c2-net-routing
package router

import (
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/transport"
)

// Proactive head-of-line (HoL) retransmit.
//
// The mux carries ONE app connection's ordered byte-stream, framed and striped
// across N legs under a single global sequence. The receiver's reorderBuffer
// NEVER skips a gap (delivering past a missing seq corrupts the ordered
// stream), so when the frontier's next-needed seq is on a slow/lagging leg the
// whole stream stalls while already-arrived frames from fast legs sit buffered.
//
// The existing SACK path DOES eventually heal this: the receiver reports the
// hole and the sender retransmits it on the fastest live leg
// (resendSeqs -> selectFastestTransport). But it is REACTIVE and slow — the
// sender's retxMinAge (750ms) presumes a missing seq is merely in flight on a
// slower leg and refuses to retransmit before then, and the no-arrival fallback
// (reorderStallServiceFn) only fires after reorderTimeout (1.5s). Either way the
// stall is bounded by hundreds of ms, so adding legs makes a single download
// STRICTLY WORSE.
//
// Proactive HoL retransmit bounds the stall to ~one FAST-leg RTT instead: when
// the frontier gap has stayed open longer than roughly one fastest-live-leg RTT
// (with a low floor of a few ms), the sender retransmits the frontier-blocking
// seq (and the next few contiguous missing seqs) IMMEDIATELY on the fastest live
// leg, without waiting out retxMinAge or the slow leg. It reuses the SACK wire
// message verbatim (the SACK's lastContiguous conveys the stuck frontier =
// lastContiguous+1); nothing new goes on the wire. The whole behavior is gated
// behind CapHOLRetx negotiated at handshake, so a peer that doesn't advertise it
// cleanly falls back to today's reactive SACK behavior. The no-skip invariant is
// untouched — this makes the gap FILL faster, it never skips it.

const (
	// holRetxGapFloor is the low floor on the frontier-gap age that triggers a
	// proactive HoL retransmit. Below any realistic inter-leg latency skew a gap
	// is ordinary interleave, not a stall, so never nudge faster than this even
	// when the fastest leg's RTT is tiny (LAN legs). A few ms.
	holRetxGapFloor = 4 * time.Millisecond
	// holRetxRTTFactor scales the fastest-live-leg RTT into the gap-age threshold.
	// ~1.0 == "one fast-leg RTT": long enough that genuine skew (the missing seq
	// arriving on its own slower leg) usually resolves without a retransmit, short
	// enough that a real stall is healed in about one fast round-trip.
	holRetxRTTFactor = 1.0
	// holRetxMaxFill bounds how many contiguous missing seqs one proactive nudge
	// retransmits: the frontier seq plus the next few holes behind it. Small so a
	// nudge heals the head of the stall without dumping the whole in-flight window
	// (that is what the demote-time forced flush is for), and so a single SACK
	// can't provoke a large retransmit burst.
	holRetxMaxFill = 4
	// holRetxPerSeqFloor is the low floor on the per-seq re-nudge interval, so a
	// persistently-stuck frontier seq is not resent in a storm even on tiny-RTT
	// legs. Mirrors holRetxGapFloor.
	holRetxPerSeqFloor = 4 * time.Millisecond
)

// holGapThreshold returns the frontier-gap age past which a proactive HoL
// retransmit is warranted, given the fastest live leg's RTT in ms. It is
// max(floor, RTT*factor): a few ms floor, otherwise about one fast-leg RTT. A
// non-positive RTT (no latency measured yet) falls back to the floor.
func holGapThreshold(fastestRTTms float64) time.Duration {
	if fastestRTTms <= 0 {
		return holRetxGapFloor
	}
	d := time.Duration(fastestRTTms*holRetxRTTFactor) * time.Millisecond
	if d < holRetxGapFloor {
		return holRetxGapFloor
	}
	return d
}

// holPerSeqInterval returns the minimum spacing between successive proactive
// retransmits of the SAME seq, given the fastest live leg's RTT in ms. A seq
// should not be re-nudged before a fresh retransmit could plausibly have arrived
// and been acked — about one fast-leg RTT — so this is max(floor, RTT). A
// non-positive RTT falls back to the floor.
func holPerSeqInterval(fastestRTTms float64) time.Duration {
	if fastestRTTms <= 0 {
		return holRetxPerSeqFloor
	}
	d := time.Duration(fastestRTTms) * time.Millisecond
	if d < holRetxPerSeqFloor {
		return holRetxPerSeqFloor
	}
	return d
}

// frontierMissingSeqs extracts the head-of-line region from a received SACK: the
// stuck frontier seq (lastContiguous+1) and the run of contiguous MISSING seqs
// immediately behind it, up to max. It stops at the first RECEIVED seq (a set
// bit) — those aren't blocking and the receiver already holds them — and at the
// end of the reported bitmap. Returns nil when the SACK reports no out-of-order
// window (empty bitmap): with no frames buffered above the contiguous point
// there is no HoL stall to heal, only a normal in-flight tail.
//
// Word w bit i set means seq (lastContiguous+1 + w*64 + i) has been received,
// matching sackTracker.GenerateSACK / retxBuffer.ProcessSACK.
func frontierMissingSeqs(lastContiguous uint32, words []uint64, max int) []uint32 {
	if len(words) == 0 || max <= 0 {
		return nil
	}
	var out []uint32
	for w, word := range words {
		for i := uint32(0); i < 64; i++ {
			if word&(1<<i) != 0 {
				return out // first received seq ends the contiguous frontier run
			}
			seq := lastContiguous + 1 + uint32(w)*64 + i
			out = append(out, seq)
			if len(out) >= max {
				return out
			}
		}
	}
	return out
}

// holRetxTracker is the sender-side per-seq rate limiter for proactive HoL
// retransmits. It records the last time each seq was proactively retransmitted
// so a persistently-stuck frontier seq is nudged at most once per fast-leg RTT
// (via Due), rather than resent on every SACK — the self-amplifying storm
// retxMinAge guards against on the reactive path, which the proactive path must
// guard against on its own because it deliberately bypasses retxMinAge.
type holRetxTracker struct {
	mu       sync.Mutex
	lastNano map[uint32]int64
}

func newHolRetxTracker() *holRetxTracker {
	return &holRetxTracker{lastNano: make(map[uint32]int64)}
}

// Due reports whether seq may be proactively retransmitted at time now given the
// minimum per-seq interval, and records now as the send time when it returns
// true. A seq never nudged before is always due.
func (t *holRetxTracker) Due(seq uint32, interval time.Duration, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	nowNano := now.UnixNano()
	if prev, ok := t.lastNano[seq]; ok && nowNano-prev < int64(interval) {
		return false
	}
	t.lastNano[seq] = nowNano
	return true
}

// Purge drops tracked seqs at or below belowSeq. Called with the SACK's
// lastContiguous so the map only ever holds still-outstanding frontier seqs and
// cannot grow without bound as the stream advances.
func (t *holRetxTracker) Purge(belowSeq uint32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for seq := range t.lastNano {
		if seq <= belowSeq {
			delete(t.lastNano, seq)
		}
	}
}

// fastestLegLatency returns the lowest positive measured RTT (ms) across the
// mux's live, ready, non-standby legs, or 0 when none has a measurement yet.
// This is the leg a proactive retransmit will actually ride
// (selectFastestTransport), so its RTT sets both the gap-age threshold and the
// per-seq re-nudge interval. Caller holds rg.mu (reads the tps slice).
func (m *routeMux) fastestLegLatency(tps []*transport.ManagedTransport) float64 {
	best := 0.0
	for idx, tp := range tps {
		if tp == nil || tp.IsClosed() || !m.legReadyAt(idx) {
			continue
		}
		lat := tp.GetLatency()
		if lat <= 0 {
			continue
		}
		if best == 0 || lat < best {
			best = lat
		}
	}
	return best
}

// proactiveRetxSeqs is the sender-side decision for a received SACK: which
// frontier seqs to retransmit NOW on the fastest leg, bypassing retxMinAge.
// Returns nil (a clean no-op fallback) when HoL retx was not negotiated, so a
// peer without CapHOLRetx keeps today's purely reactive behavior. Otherwise it
// takes the head-of-line run from the SACK (frontierMissingSeqs), keeps only the
// seqs we still hold in the retx buffer, and per-seq rate-limits them to one
// nudge per fast-leg RTT (holRetxTracker.Due). fastestRTTms is the fastest live
// leg's measured RTT, used for the per-seq interval. It also purges the tracker
// below lastContiguous so it can't grow without bound.
func (m *routeMux) proactiveRetxSeqs(lastContiguous uint32, words []uint64, fastestRTTms float64, now time.Time) []uint32 {
	if !m.holRetxEnabled || m.retxBuf == nil || m.holRetx == nil {
		return nil
	}
	m.holRetx.Purge(lastContiguous)
	cand := frontierMissingSeqs(lastContiguous, words, holRetxMaxFill)
	if len(cand) == 0 {
		return nil
	}
	interval := holPerSeqInterval(fastestRTTms)
	var due []uint32
	for _, seq := range cand {
		// Only retransmit seqs we still hold: a seq already purged from the retx
		// buffer was acknowledged, so it is not the one blocking the frontier.
		if m.retxBuf.Get(seq) == nil {
			continue
		}
		if m.holRetx.Due(seq, interval, now) {
			due = append(due, seq)
		}
	}
	return due
}
