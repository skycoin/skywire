// Package router pkg/router/route_mux_record.go c2-net-routing
package router

import (
	"time"
)

// recordSent atomically increments the sent-bytes/packets counters
// for leg idx. Bounds-checked; out-of-range indices are silently
// dropped (defensive: a leg can be removed between selectTransport
// and the actual write returning).
func (m *routeMux) recordSent(idx int, n uint64) {
	if idx < 0 {
		return
	}
	m.legMu.RLock()
	if idx < len(m.legs) {
		m.legs[idx].sentBytes.Add(n)
		m.legs[idx].sentPackets.Add(1)
		// The probe budget is charged from what actually went on the wire, so a
		// leg ruled probe-only carries one probe per window however large the
		// frames are (the gate's own charge would have to guess the size).
		m.legs[idx].probeWinBytes.Add(n)
	}
	m.legMu.RUnlock()
}

// recordRecv atomically increments the recv-bytes/packets counters
// for leg idx. Same bounds-check semantics as recordSent.
func (m *routeMux) recordRecv(idx int, n uint64) {
	if idx < 0 {
		return
	}
	m.legMu.RLock()
	if idx < len(m.legs) {
		m.legs[idx].recvBytes.Add(n)
		m.legs[idx].recvPackets.Add(1)
		m.legs[idx].lastRecvNano.Store(time.Now().UnixNano())
	}
	m.legMu.RUnlock()
}

// sackLeg picks the leg a receiver-side SACK rides. The primary (0), as it
// always has — unless the primary has received nothing for `silence` while
// another leg has received since; then the leg that received last. A SACK
// pinned to the primary is lost whole when the primary is black-holed in both
// directions: the sender hears no loss, its windows fill with frames nobody
// will ever acknowledge, and the group delivers nothing more over a survivor
// that is perfectly healthy (TestEmuBothWayCutOfALegCompletes; the 2026-09-23
// rig's 100 MB row stalled at 2 MiB). A leg that is receiving is a leg whose
// path answers, so its reverse direction is the best evidence available.
func (m *routeMux) sackLeg(silence time.Duration, now int64) int {
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	if len(m.legs) < 2 || m.legs[0] == nil {
		return 0
	}
	primary := m.legs[0].lastRecvNano.Load()
	if now-primary < int64(silence) {
		return 0
	}
	best, bestAt := 0, primary
	for i := 1; i < len(m.legs); i++ {
		if m.legs[i] == nil {
			continue
		}
		if at := m.legs[i].lastRecvNano.Load(); at > bestAt {
			best, bestAt = i, at
		}
	}
	return best
}

// recordPayload atomically credits leg idx with n bytes of UNIQUE in-order
// payload (a seq's first arrival). Same bounds-check semantics as recordRecv.
func (m *routeMux) recordPayload(idx int, n uint64) {
	if idx < 0 {
		return
	}
	m.legMu.RLock()
	if idx < len(m.legs) {
		m.legs[idx].payloadBytes.Add(n)
	}
	m.legMu.RUnlock()
}

// recordDup atomically credits leg idx with n bytes of DUPLICATE data (a seq
// that had already been delivered or buffered when it arrived on this leg).
// Splitting dupBytes out of recvBytes is what attributes a "standby leg with
// traffic" honestly: the peer's spurious retransmits ride the fastest leg
// (resendSeqs → selectFastestTransport), which is typically the parked direct
// leg, and without this counter that flow is indistinguishable from striped
// payload. Same bounds-check semantics as recordRecv.
func (m *routeMux) recordDup(idx int, n uint64) {
	if idx < 0 {
		return
	}
	m.legMu.RLock()
	if idx < len(m.legs) {
		m.legs[idx].dupBytes.Add(n)
	}
	m.legMu.RUnlock()
}

// recordRepair atomically credits leg idx with n bytes of FEC repair frames.
// Repairs are overhead by design (they buy gap-fill latency); counting them
// per leg separates that deliberate cost from spurious-retransmit waste.
func (m *routeMux) recordRepair(idx int, n uint64) {
	if idx < 0 {
		return
	}
	m.legMu.RLock()
	if idx < len(m.legs) {
		m.legs[idx].repairBytes.Add(n)
	}
	m.legMu.RUnlock()
}

// recordRetransmit atomically increments the retransmit counter for leg
// idx (the leg that carried a SACK retransmit). The retransmitted bytes
// are still recorded via recordSent; this is the separate loss signal.
func (m *routeMux) recordRetransmit(idx int) {
	if idx < 0 {
		return
	}
	m.legMu.RLock()
	if idx < len(m.legs) {
		m.legs[idx].retransmits.Add(1)
	}
	m.legMu.RUnlock()
}

// retransmitsAt returns leg idx's cumulative retransmit count (0 if out of
// range), for snapshotLegs / LegInfo without a full Snapshot allocation.
func (m *routeMux) retransmitsAt(idx int) uint64 {
	if idx < 0 {
		return 0
	}
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	if idx < len(m.legs) {
		return m.legs[idx].retransmits.Load()
	}
	return 0
}

// retxStats exposes the sender-side retx buffer's occupancy and the sequence
// range it still holds (0,0,0 when SACK/retx is not in play). The receiver
// naming a stuck frontier seq is only half a wedge diagnosis; this says whether
// the sender can still honor a retransmit request for it.
func (m *routeMux) retxStats() (held int, minSeq, maxSeq uint32) {
	if m.retxBuf == nil {
		return 0, 0, 0
	}
	return m.retxBuf.Stats()
}
