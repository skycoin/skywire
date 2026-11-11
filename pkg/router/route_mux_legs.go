// Package router pkg/router/route_mux_legs.go c2-net-routing
package router

import (
	"time"

	"github.com/google/uuid"
)

// appendLegLocked adds the record of a new last leg. Leg 0 is ready and never
// standby. An aux leg starts not-ready, and standby when standbyNewLegs is set.
func (m *routeMux) appendLegLocked(id uuid.UUID) {
	aux := len(m.legs) > 0
	m.legs = append(m.legs, &muxLeg{id: id, ready: !aux, standby: aux && m.standbyNewLegs, group: noLegGroup})
}

// syncLegs brings the records in step with the rg's legs, given as their
// transport IDs in leg order. It adds a record per new leg and names unnamed ones.
func (m *routeMux) syncLegs(ids []uuid.UUID) {
	m.legMu.Lock()
	for i, id := range ids {
		if i >= len(m.legs) {
			m.appendLegLocked(id)
		} else if m.legs[i].id == uuid.Nil {
			m.legs[i].id = id
		}
	}
	m.legMu.Unlock()
}

// legAt returns the record of leg idx, or nil when there is none. Call it under
// rg.mu, while idx still names the leg; the record stays the leg's afterwards.
func (m *routeMux) legAt(idx int) *muxLeg {
	if idx < 0 {
		return nil
	}
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	if idx >= len(m.legs) {
		return nil
	}
	return m.legs[idx]
}

// SetStandbyNewLegs controls whether newly-grown aux legs enter warm standby on
// add (see the standbyNewLegs field). Called by the route group when a promoting
// rotation engine is wired, before aux legs are appended. Idempotent.
func (m *routeMux) SetStandbyNewLegs(v bool) {
	m.legMu.Lock()
	m.standbyNewLegs = v
	m.legMu.Unlock()
}

// SetLegGroups records each leg's shared-bottleneck group id, in leg order. A
// leg past the end of groups is its own group, and nil clears the grouping.
func (m *routeMux) SetLegGroups(groups []int) {
	m.legMu.Lock()
	for i, l := range m.legs {
		l.group = noLegGroup
		if i < len(groups) {
			l.group = groups[i]
		}
	}
	m.legMu.Unlock()
}

// clearLegGroupsLocked drops the grouping. Group ids are leg indices, so it does
// not survive a removal or a swap. Caller holds legMu.
func (m *routeMux) clearLegGroupsLocked() {
	for _, l := range m.legs {
		l.group = noLegGroup
	}
}

// groupOf returns leg i's shared-bottleneck group id, i itself when none is
// recorded. Caller holds legMu.
func (m *routeMux) groupOf(i int) int {
	if i < len(m.legs) && m.legs[i].group != noLegGroup {
		return m.legs[i].group
	}
	return i
}

// removeLegs drops the records at the given ORIGINAL leg indices, after rg.legs
// lost the same legs. It reports whether the leg now in slot 0 was standby.
func (m *routeMux) removeLegs(indices ...int) (promoted bool) {
	if len(indices) == 0 {
		return false
	}
	drop := make(map[int]bool, len(indices))
	for _, i := range indices {
		drop[i] = true
	}
	m.legMu.Lock()
	defer m.legMu.Unlock()
	m.clearLegGroupsLocked()
	if len(m.legs) == 0 {
		return false
	}
	kept := make([]*muxLeg, 0, len(m.legs))
	for i, l := range m.legs {
		if !drop[i] {
			kept = append(kept, l)
		}
	}
	m.legs = kept
	return m.assertPrimaryLocked()
}

// assertPrimaryLocked makes the leg in slot 0 ready and not standby, and reports
// whether it was standby. Caller holds legMu.
func (m *routeMux) assertPrimaryLocked() (wasStandby bool) {
	if len(m.legs) == 0 {
		return false
	}
	wasStandby = m.legs[0].standby
	m.legs[0].ready, m.legs[0].standby = true, false
	return wasStandby
}

// markLegReady records that leg idx has carried inbound traffic, so it
// is now safe to select for sending. Idempotent and bounds-checked.
func (m *routeMux) markLegReady(idx int) {
	if idx < 0 {
		return
	}
	m.legMu.Lock()
	if idx < len(m.legs) {
		m.legs[idx].ready = true
	}
	m.legMu.Unlock()
}

// legReadyAt reports whether leg idx may be selected for sending: ready and not
// standby. A leg with no record is not ready, except the primary.
func (m *routeMux) legReadyAt(idx int) bool {
	if idx < 0 {
		return false
	}
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	if idx >= len(m.legs) {
		return idx == 0
	}
	return m.legs[idx].ready && !m.legs[idx].standby
}

// legSelectableIgnoringStandby is legReadyAt without the standby exclusion, for
// the last-resort failover pick when no active leg is live.
func (m *routeMux) legSelectableIgnoringStandby(idx int) bool {
	if idx < 0 {
		return false
	}
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	if idx >= len(m.legs) {
		return idx == 0
	}
	return m.legs[idx].ready
}

// setLegStandby parks or promotes leg idx. Leg 0 is never standby. A leg the
// mux has no record of yet gets one, so an explicit promotion is not lost.
func (m *routeMux) setLegStandby(idx int, standby bool) {
	if idx <= 0 {
		return // leg 0 is never standby
	}
	m.legMu.Lock()
	for len(m.legs) <= idx {
		m.appendLegLocked(uuid.Nil)
	}
	m.legs[idx].standby = standby
	m.legMu.Unlock()
	m.signalWindow()
}

// parkAllAuxStandby marks every aux leg (index > 0) as warm standby. Used by the
// acceptor when leg-state signaling is negotiated so its active set starts EMPTY
// and is filled only by the initiator's mirror promotes — the acceptor must never
// send the bulk direction across a leg the initiator hasn't activated. Leg 0 (the
// primary) is left active.
func (m *routeMux) parkAllAuxStandby() {
	m.legMu.Lock()
	for i := 1; i < len(m.legs); i++ {
		m.legs[i].standby = true
	}
	m.legMu.Unlock()
}

// swapLegs exchanges the records of two legs, in step with the swap of rg.legs
// in reelectPrimary. A no-op if either index is out of range or i == j.
func (m *routeMux) swapLegs(i, j int) {
	if i == j || i < 0 || j < 0 {
		return
	}
	m.legMu.Lock()
	defer m.legMu.Unlock()
	m.clearLegGroupsLocked()
	if i < len(m.legs) && j < len(m.legs) {
		m.legs[i], m.legs[j] = m.legs[j], m.legs[i]
	}
	m.assertPrimaryLocked()
}

// activeLegCount reports how many legs are currently active (not warm standby)
// — the striped set width. Used for wedge/hold diagnostics.
func (m *routeMux) activeLegCount() int {
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	active := 0
	for _, l := range m.legs {
		if !l.standby {
			active++
		}
	}
	return active
}

// isLegStandby reports whether leg idx is a warm standby. Bounds-checked.
func (m *routeMux) isLegStandby(idx int) bool {
	if idx < 0 {
		return false
	}
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	return idx < len(m.legs) && m.legs[idx].standby
}

// snapshotLegs returns a stable copy of the current per-leg counters and, as a
// side effect, samples each leg's goodput RATE (bytes/sec EWMA) over the window
// since the previous snapshot. The byte/packet counters are point-in-time
// atomic loads; the rate is maintained under legMu (a write lock) so concurrent
// observers don't corrupt the per-leg sample state. Called at telemetry/UI
// cadence (the status page's ~1s push, CLI mux-info), never on the data path.
func (m *routeMux) snapshotLegs() []LegStats {
	now := time.Now().UnixNano()
	m.legMu.Lock()
	out := make([]LegStats, len(m.legs))
	for i, c := range m.legs {
		sent := c.sentBytes.Load()
		recv := c.recvBytes.Load()
		m.sampleGoodput(&c.legCounters, sent, recv, now)
		out[i] = LegStats{
			Index:          i,
			SentBytes:      sent,
			SentPackets:    c.sentPackets.Load(),
			RecvBytes:      recv,
			RecvPackets:    c.recvPackets.Load(),
			PayloadBytes:   c.payloadBytes.Load(),
			DupBytes:       c.dupBytes.Load(),
			RepairBytes:    c.repairBytes.Load(),
			Retransmits:    c.retransmits.Load(),
			GoodputUpBps:   c.goodputUpBps,
			GoodputDownBps: c.goodputDownBps,
			GoodputBps:     c.goodputUpBps + c.goodputDownBps,
		}
	}
	m.legMu.Unlock()
	return out
}

// sampleGoodput updates leg c's per-direction goodput EWMAs from the sent and
// recv byte counters observed at wall-clock now (UnixNano). Caller holds legMu
// for writing. The first observation only seeds the baseline (no rate emitted).
// To keep the metric stable when several observers interleave, samples closer
// together than goodputMinSampleNano are skipped and the stored rates are left
// unchanged.
func (m *routeMux) sampleGoodput(c *legCounters, sent, recv uint64, now int64) {
	if c.lastRateNano == 0 {
		c.lastRateSentBytes = sent
		c.lastRateRecvBytes = recv
		c.lastRateNano = now
		return
	}
	elapsed := now - c.lastRateNano
	if elapsed < goodputMinSampleNano {
		return
	}
	secs := float64(elapsed) / float64(time.Second)
	c.goodputUpBps = ewmaRate(c.goodputUpBps, byteDelta(sent, c.lastRateSentBytes), secs)
	c.goodputDownBps = ewmaRate(c.goodputDownBps, byteDelta(recv, c.lastRateRecvBytes), secs)
	c.lastRateSentBytes = sent
	c.lastRateRecvBytes = recv
	c.lastRateNano = now
}
