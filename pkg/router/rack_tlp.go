// Package router pkg/router/rack_tlp.go c2-net-routing
package router

import (
	"sync/atomic"
	"time"
)

// RACK-TLP (RFC 8985) completes the mux's loss recovery beyond the RTT-derived
// retransmit threshold (rackThreshold, route_mux.go) with two pieces:
//
//   - Tail-Loss Probe (TLP): the SACK path only recovers a hole once a LATER
//     sequence arrives to expose it. If the TAIL of a burst is lost there is no
//     later sequence — the receiver's bitmap simply ends at the last seq it got,
//     the sender treats the missing tail as still-in-flight, and the stream stalls
//     until the retx buffer ages the entry out. TLP fixes this: after the sender
//     goes idle for a probe timeout (PTO ≈ 2×maxLegRTT) with unacked data
//     outstanding, it re-sends the tail sequence as a probe. That probe either
//     fills the loss or draws a SACK that finally reports the gap, so normal
//     recovery proceeds — turning a multi-hundred-ms stall into one PTO.
//
//   - DSACK reorder-window adaptation: when the receiver reports a DUPLICATE
//     (a DSACK — see sackTracker), the sender learns it retransmitted a sequence
//     that was merely reordered on a slower leg, not lost. It widens the reorder
//     factor (rackFactor) so rackThreshold waits longer before the next
//     retransmit; clean acks decay it back toward the static baseline. This is the
//     closed loop that keeps the anti-spurious-retransmit threshold matched to the
//     path's real reordering instead of a fixed guess.

// DSACK reorder-factor adaptation bounds (milli-units: factor ×1000).
const (
	rackFactorMin     = int64(rackReorderFactor * 1000) // baseline (== 1.25×)
	rackFactorMax     = 3000                            // cap the widening at 3.0× maxLegRTT
	rackDSACKGrowStep = 250                             // widen per DSACK (a spurious retransmit observed)
	rackDecayStep     = 25                              // narrow per clean SACK, back toward baseline
)

// Tail-Loss Probe timing bounds.
const (
	tlpPTOFactor     = 2.0                    // PTO ≈ this × slowest active-leg RTT (RFC 8985)
	tlpMinPTO        = 100 * time.Millisecond // never probe sooner than this (anti-spurious)
	tlpMaxPTO        = 2 * time.Second        // never wait longer than this before probing
	tlpMaxProbes     = 2                      // consecutive probes per stall before deferring to other recovery
	tlpCheckInterval = 100 * time.Millisecond // service-loop cadence (idle check; sends only when a probe is due)
)

// rackFactor returns the current DSACK-adapted reorder factor (slow-leg RTT is
// multiplied by this in rackThreshold). Falls back to the static baseline if the
// adaptive value was never initialized.
func (m *routeMux) rackFactor() float64 {
	v := atomic.LoadInt64(&m.rackFactorMilli)
	if v <= 0 {
		return rackReorderFactor
	}
	return float64(v) / 1000
}

// growRackFactor widens the reorder factor after a DSACK (the receiver saw a
// duplicate ⇒ our retransmit was spurious ⇒ be more reorder-tolerant). Bounded at
// rackFactorMax. Concurrency-safe (CAS loop).
func (m *routeMux) growRackFactor(dsackSeq uint32) {
	for {
		cur := atomic.LoadInt64(&m.rackFactorMilli)
		next := cur + rackDSACKGrowStep
		if next > rackFactorMax {
			next = rackFactorMax
		}
		if next == cur {
			return
		}
		if atomic.CompareAndSwapInt64(&m.rackFactorMilli, cur, next) {
			if m.logger != nil {
				m.logger.Debugf("RACK: DSACK seq=%d (spurious retransmit) → reorder factor widened to %.2f×", dsackSeq, float64(next)/1000)
			}
			return
		}
	}
}

// decayRackFactor narrows the reorder factor one step toward the baseline on a
// clean SACK (no duplicate reported), so a transient reordering episode widens the
// window and then relaxes once the path settles. Never drops below the baseline.
// Concurrency-safe (CAS loop).
func (m *routeMux) decayRackFactor() {
	for {
		cur := atomic.LoadInt64(&m.rackFactorMilli)
		if cur <= rackFactorMin {
			return
		}
		next := cur - rackDecayStep
		if next < rackFactorMin {
			next = rackFactorMin
		}
		if atomic.CompareAndSwapInt64(&m.rackFactorMilli, cur, next) {
			return
		}
	}
}

// onSACKReceived is the sender-side SACK entry point: it advances the ack-progress
// edge (resetting the TLP probe budget when the contiguous point moves), adapts
// the reorder factor from the DSACK signal, and returns the sequences to
// retransmit. Replaces a bare processSACK call so all three effects stay in step.
func (m *routeMux) onSACKReceived(lastContig uint32, words []uint64, dsackSeq uint32, hasDSACK bool) []uint32 {
	if !m.sackEnabled || m.retxBuf == nil {
		return nil
	}
	// Ack progress: the contiguous frontier moved, so the tail is advancing —
	// clear the probe budget so a later stall is treated as a fresh event.
	if prev := atomic.LoadUint32(&m.lastAckedContig); lastContig > prev {
		atomic.StoreUint32(&m.lastAckedContig, lastContig)
		atomic.StoreInt32(&m.tlpProbeCount, 0)
	}
	if hasDSACK {
		m.growRackFactor(dsackSeq)
	} else {
		m.decayRackFactor()
	}
	return m.retxBuf.ProcessSACK(lastContig, words, m.rackThreshold())
}

// ptoInterval is the tail-loss probe timeout: how long the sender stays idle with
// unacked data before it probes. Derived from the slowest active leg's RTT (a
// frame should have been acked within one RTT + margin), floored/capped.
func (m *routeMux) ptoInterval() time.Duration {
	maxRtt := m.maxActiveLegRTTms()
	if maxRtt <= 0 {
		return rackDefaultNoRTT * 2 // no RTT measured yet: conservative
	}
	pto := time.Duration(maxRtt*tlpPTOFactor) * time.Millisecond
	if pto < tlpMinPTO {
		pto = tlpMinPTO
	}
	if pto > tlpMaxPTO {
		pto = tlpMaxPTO
	}
	return pto
}

// tlpProbeSeq reports the tail sequence to probe now, or (0,false) if no probe is
// due. A probe is due when SACK is on, unacked data is outstanding, the sender has
// been idle at least a PTO, and the per-stall probe budget isn't exhausted. It
// increments the probe counter as a side effect, so each due check yields exactly
// one probe; the counter is reset by onSACKReceived when the ack frontier moves.
func (m *routeMux) tlpProbeSeq(now time.Time) (uint32, bool) {
	if !m.sackEnabled || m.retxBuf == nil {
		return 0, false
	}
	tail, ok := m.retxBuf.MaxSeq()
	if !ok {
		return 0, false // nothing outstanding — no tail to probe
	}
	if atomic.LoadInt32(&m.tlpProbeCount) >= tlpMaxProbes {
		return 0, false // budget spent; defer to reactive SACK / retx aging
	}
	last := atomic.LoadInt64(&m.lastSendNano)
	if last == 0 {
		return 0, false // nothing sent yet
	}
	if now.Sub(time.Unix(0, last)) < m.ptoInterval() {
		return 0, false // not idle long enough
	}
	atomic.AddInt32(&m.tlpProbeCount, 1)
	return tail, true
}
