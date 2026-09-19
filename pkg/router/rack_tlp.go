// Package router pkg/router/rack_tlp.go c2-net-routing
package router

import (
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/router/routersettings"
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

// DSACK reorder-factor adaptation bounds (milli-units: factor ×1000). The
// baseline is rack.reorder_factor itself — see (*routeMux).rackFactorMin.
const (
	rackFactorMaxDefault     = 3000 // cap the widening at 3.0× maxLegRTT
	rackDSACKGrowStepDefault = 250  // widen per DSACK (a spurious retransmit observed)
	rackDecayStepDefault     = 25   // narrow per clean SACK, back toward baseline
)

// Tail-Loss Probe timing bounds.
const (
	tlpPTOFactorDefault     = 2.0                    // PTO ≈ this × slowest active-leg RTT (RFC 8985)
	tlpMinPTODefault        = 100 * time.Millisecond // never probe sooner than this (anti-spurious)
	tlpMaxPTODefault        = 2 * time.Second        // never wait longer than this before probing
	tlpMaxProbesDefault     = 2                      // consecutive probes per stall before deferring to other recovery
	tlpCheckIntervalDefault = 100 * time.Millisecond // service-loop cadence (idle check; sends only when a probe is due)
)

// rackFactor returns the current DSACK-adapted reorder factor (slow-leg RTT is
// multiplied by this in rackThreshold). Falls back to the static baseline if the
// adaptive value was never initialized.
func (m *routeMux) rackFactor() float64 {
	v := m.rackFactorMilli.Load()
	if v <= 0 {
		return m.knRatio(routersettings.RackReorderFactor)
	}
	return float64(v) / 1000
}

// growRackFactor widens the reorder factor after a DSACK (the receiver saw a
// duplicate ⇒ our retransmit was spurious ⇒ be more reorder-tolerant). Bounded at
// rackFactorMax. Concurrency-safe (CAS loop).
func (m *routeMux) growRackFactor(dsackSeq uint32) {
	for {
		cur := m.rackFactorMilli.Load()
		next := cur + int64(m.knInt(routersettings.RackDSACKGrowStep))
		if next > m.rackFactorMax() {
			next = m.rackFactorMax()
		}
		if next == cur {
			return
		}
		if m.rackFactorMilli.CompareAndSwap(cur, next) {
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
		cur := m.rackFactorMilli.Load()
		if cur <= m.rackFactorMin() {
			return
		}
		next := cur - int64(m.knInt(routersettings.RackDecayStep))
		if next < m.rackFactorMin() {
			next = m.rackFactorMin()
		}
		if m.rackFactorMilli.CompareAndSwap(cur, next) {
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
		// The duplicate proves the ORIGINAL arrived after we had already
		// retransmitted it, so its whole send→ack delay is a real delivery-delay
		// sample — the one ProcessSACK cannot take (Karn: a retransmitted entry's
		// ack is ambiguous there). Without it a storm sustains itself: once the
		// threshold undershoots the loaded delay every frame is re-sent before its
		// ack, the never-retransmitted sample set empties, and the estimate that
		// would lift the threshold never moves (measured live 2026-09-16: 37207
		// retransmits for 20304 frames on a three-leg group).
		if sentAt, tpID, ok := m.retxBuf.SentInfo(dsackSeq); ok {
			d := time.Since(sentAt)
			m.recordAckDelay(d)
			if tpID != uuid.Nil {
				m.recordAckDelayTp(tpID, d)
			}
		}
		m.growRackFactor(dsackSeq)
	} else {
		m.decayRackFactor()
	}
	th := m.rackThreshold() // computed BEFORE the buffer lock: the per-leg callback must not read the leg table under it
	retx, deferred := m.retxBuf.ProcessSACKWith(lastContig, words, th, func(tpID uuid.UUID) time.Duration { return m.rackThresholdForWith(th, tpID) })
	if deferred > 0 {
		m.retxDeferredYoung.Add(uint64(deferred))
	}
	m.signalWindow() // purged entries may have freed per-leg window for a parked writer
	return retx
}

// ptoInterval is the tail-loss probe timeout: how long the sender stays idle with
// unacked data before it probes. Derived from the slowest active leg's RTT (a
// frame should have been acked within one RTT + margin), floored/capped.
func (m *routeMux) ptoInterval() time.Duration {
	maxRtt := m.maxActiveLegRTTms()
	if maxRtt <= 0 {
		return m.knDur(routersettings.RackDefaultNoRTT) * 2 // no RTT measured yet: conservative
	}
	pto := time.Duration(maxRtt*m.knRatio(routersettings.TLPPTOFactor)) * time.Millisecond
	if pto < m.knDur(routersettings.TLPMinPTO) {
		pto = m.knDur(routersettings.TLPMinPTO)
	}
	if pto > m.knDur(routersettings.TLPMaxPTO) {
		pto = m.knDur(routersettings.TLPMaxPTO)
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
	if int(atomic.LoadInt32(&m.tlpProbeCount)) >= m.knInt(routersettings.TLPMaxProbes) {
		return 0, false // budget spent; defer to reactive SACK / retx aging
	}
	last := m.lastSendNano.Load()
	if last == 0 {
		return 0, false // nothing sent yet
	}
	if now.Sub(time.Unix(0, last)) < m.ptoInterval() {
		return 0, false // not idle long enough
	}
	atomic.AddInt32(&m.tlpProbeCount, 1)
	return tail, true
}

// rackFactorMin is the DSACK adaptation's baseline in milli-units: the live
// rack.reorder_factor, which the widening never narrows back below.
func (m *routeMux) rackFactorMin() int64 {
	return int64(m.knRatio(routersettings.RackReorderFactor) * 1000)
}

// rackFactorMax is the ceiling on the DSACK-driven widening, in milli-units.
func (m *routeMux) rackFactorMax() int64 {
	return int64(m.knRatio(routersettings.RackFactorMax) * 1000)
}
