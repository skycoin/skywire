// Package router pkg/router/route_mux_legs.go c2-net-routing
package router

import (
	"time"
)

// growLegs extends the per-leg counter slice to cover at least n
// legs. Called when transports are appended to the rg (initial setup
// + AppendRoute). Idempotent — extending past the current size is a
// no-op for legs that already exist.
func (m *routeMux) growLegs(n int) {
	m.legMu.Lock()
	for len(m.legs) < n {
		m.legs = append(m.legs, &legCounters{})
	}
	for len(m.ready) < n {
		// The primary leg (index 0) is ready immediately; aux legs start
		// not-ready and are marked ready on the first inbound packet.
		m.ready = append(m.ready, len(m.ready) == 0)
	}
	for len(m.standby) < n {
		// The primary leg (index 0, the first append) is always active. Aux
		// legs enter warm standby when standbyNewLegs is set (a promoting
		// rotation engine is wired), so the engine promotes them one per tick
		// as its goodput signal warrants instead of all going hot at once.
		m.standby = append(m.standby, m.standbyNewLegs && len(m.standby) > 0)
	}
	m.legMu.Unlock()
}

// SetStandbyNewLegs controls whether newly-grown aux legs enter warm standby on
// add (see the standbyNewLegs field). Called by the route group when a promoting
// rotation engine is wired, before aux legs are appended. Idempotent.
func (m *routeMux) SetStandbyNewLegs(v bool) {
	m.legMu.Lock()
	m.standbyNewLegs = v
	m.legMu.Unlock()
}

// SetLegGroups records the per-leg shared-bottleneck group ids (see the
// legGroups field). The slice is copied and is parallel to legs[] in the rg's
// tps[] order; a shorter slice leaves the trailing legs as singletons. Cheap and
// idempotent — called each data-progress tick from the route group after it
// recomputes groups from per-leg OWD statistics.
func (m *routeMux) SetLegGroups(groups []int) {
	m.legMu.Lock()
	m.legGroups = append([]int(nil), groups...)
	m.legMu.Unlock()
}

// groupOf returns leg i's shared-bottleneck group id, defaulting to i itself
// (its own singleton group) when no grouping is recorded for it. Caller holds
// legMu.
func (m *routeMux) groupOf(i int) int {
	if i < len(m.legGroups) {
		return m.legGroups[i]
	}
	return i
}

// removeLegs drops the given ORIGINAL leg indices from legs[] and ready[] so
// they stay aligned with the rg's compacted tps[]/fwd[]/rvs[] after a leg is
// removed (RemoveMuxRouteByTransport / pruneDeadTransports). It rebuilds both
// slices skipping the dropped indices (order-independent), then re-asserts the
// leg-0-always-ready invariant — leg 0 may have been promoted from an aux when
// a primary transport is pruned. Without this lockstep compaction the arrays
// desync from tps[]: readiness and per-leg accounting attach to the wrong leg,
// which can flip a live leg to not-ready (the mux>=2 hang — see the ready[]
// note above) or mis-attribute bytes after an index is reused. The counterpart
// to growLegs.
func (m *routeMux) removeLegs(indices ...int) {
	if len(indices) == 0 {
		return
	}
	drop := make(map[int]bool, len(indices))
	for _, i := range indices {
		drop[i] = true
	}
	m.legMu.Lock()
	if len(m.legs) > 0 {
		kept := make([]*legCounters, 0, len(m.legs))
		for i, c := range m.legs {
			if !drop[i] {
				kept = append(kept, c)
			}
		}
		m.legs = kept
	}
	if len(m.ready) > 0 {
		kept := make([]bool, 0, len(m.ready))
		for i, r := range m.ready {
			if !drop[i] {
				kept = append(kept, r)
			}
		}
		m.ready = kept
		if len(m.ready) > 0 {
			m.ready[0] = true // the (possibly newly-promoted) primary is always ready
		}
	}
	if len(m.standby) > 0 {
		kept := make([]bool, 0, len(m.standby))
		for i, s := range m.standby {
			if !drop[i] {
				kept = append(kept, s)
			}
		}
		m.standby = kept
		if len(m.standby) > 0 {
			m.standby[0] = false // the primary leg is never standby
		}
	}
	m.legMu.Unlock()
}

// markLegReady records that leg idx has carried inbound traffic, so it
// is now safe to select for sending. Idempotent and bounds-checked.
func (m *routeMux) markLegReady(idx int) {
	if idx < 0 {
		return
	}
	m.legMu.Lock()
	if idx < len(m.ready) {
		m.ready[idx] = true
	}
	m.legMu.Unlock()
}

// legReadyAt reports whether leg idx may be selected for sending.
// Out-of-range indices and the never-grown case report not-ready, except
// the primary leg (0) which is always ready so a group with no readiness
// info still sends on its primary route.
func (m *routeMux) legReadyAt(idx int) bool {
	if idx < 0 {
		return false
	}
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	// A warm-standby leg is never selected for sending, regardless of
	// readiness — its rules stay installed but it carries no forward traffic.
	if idx < len(m.standby) && m.standby[idx] {
		return false
	}
	if idx >= len(m.ready) {
		return idx == 0
	}
	return m.ready[idx]
}

// legSelectableIgnoringStandby reports whether leg idx may carry a packet as an
// EMERGENCY FAILOVER target — the same readiness gate as legReadyAt but WITHOUT
// the warm-standby exclusion. A parked leg keeps its rules installed and its
// transport alive, and was ready (peer-confirmed) before it was parked, so it
// can carry traffic the instant no active leg is available. selectTransport uses
// this only as a last resort, after every active leg has been found dead/not-
// ready, so the connection never dies while ANY leg in the group is alive.
func (m *routeMux) legSelectableIgnoringStandby(idx int) bool {
	if idx < 0 {
		return false
	}
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	if idx >= len(m.ready) {
		return idx == 0
	}
	return m.ready[idx]
}

// setLegStandby marks (or clears) leg idx as a warm standby: kept alive but
// not selected for sending. Bounds-checked; the primary leg (0) cannot be put
// on standby (a group must always have a selectable send leg). Clearing the
// flag PROMOTES the leg back to active instantly, with no route setup.
func (m *routeMux) setLegStandby(idx int, standby bool) {
	if idx <= 0 {
		return // leg 0 is never standby
	}
	m.legMu.Lock()
	// The standby slice grows lazily as legs are selected, so a leg appended a
	// moment ago may have no slot yet — a bounded write then silently dropped an
	// explicit promotion (the operator-pinned second leg of a two-leg set stayed
	// parked). Grow to cover idx, filling the gap with the add-time default.
	for len(m.standby) <= idx {
		m.standby = append(m.standby, m.standbyNewLegs && len(m.standby) > 0)
	}
	m.standby[idx] = standby
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
	for i := 1; i < len(m.standby); i++ {
		m.standby[i] = true
	}
	m.legMu.Unlock()
}

// swapLegs exchanges the mux's per-leg state (counters, readiness, standby
// marker) between two leg indices so the primary slot (0) can be RE-ELECTED
// onto a healthier leg without tearing down any route. The caller (RouteGroup.
// reelectPrimary) swaps the parallel tps[]/fwd[]/rvs[] entries in the same
// critical section, so leg accounting and rules stay attached to their own
// transport across the swap. After the swap the new primary (whatever leg landed
// at index 0) is forced ready and out of standby — it is chosen from the active,
// carrying set, so this is belt-and-suspenders, not a state change. Bounds-
// checked; a no-op if either index is out of range or i == j.
func (m *routeMux) swapLegs(i, j int) {
	if i == j || i < 0 || j < 0 {
		return
	}
	m.legMu.Lock()
	defer m.legMu.Unlock()
	if i < len(m.legs) && j < len(m.legs) {
		m.legs[i], m.legs[j] = m.legs[j], m.legs[i]
	}
	if i < len(m.ready) && j < len(m.ready) {
		m.ready[i], m.ready[j] = m.ready[j], m.ready[i]
	}
	if i < len(m.standby) && j < len(m.standby) {
		m.standby[i], m.standby[j] = m.standby[j], m.standby[i]
	}
	// Re-assert the leg-0 invariants: the primary is always ready and never
	// standby. (The re-elected leg was active and carrying, so this only guards
	// against a stale flag.)
	if len(m.ready) > 0 {
		m.ready[0] = true
	}
	if len(m.standby) > 0 {
		m.standby[0] = false
	}
}

// activeLegCount reports how many legs are currently active (not warm standby)
// — the striped set width. Used for wedge/hold diagnostics.
func (m *routeMux) activeLegCount() int {
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	if len(m.standby) == 0 {
		return len(m.legs)
	}
	active := 0
	for _, s := range m.standby {
		if !s {
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
	return idx < len(m.standby) && m.standby[idx]
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
		m.sampleGoodput(c, sent, recv, now)
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
