// Package router pkg/router/service_gate.go c2-net-routing
//
// Suspending a route group's service loops while they cannot do useful work.
//
// A group runs seven knob-paced service loops. Three of them tick ten and two
// times a second — send-window, tlp, reorder-stall — and on a group that is
// holding a route open without moving bytes, all three are provable no-ops:
// tlpProbeSeq bails when the retransmit buffer holds nothing outstanding,
// reorderStallServiceFn bails when the frontier has no gap, and a send window
// recomputed from unchanged inputs yields the unchanged window. Two more —
// legstate-resync and unidir-flip — iterate from leg index 1, so they are
// no-ops on a single-leg group by construction.
//
// That is ~23 timer wakeups per second per group per end spent proving there
// is nothing to do. It does not matter at one group; the standby tunnel pool
// holds dozens, and its ceiling is in the hundreds, where it is most of the
// visor's idle CPU.
//
// So those five loops take a gate. While the gate says dormant the loop stops
// its ticker and parks on a broadcast channel, costing nothing but its stack;
// the write path, the receive path and the leg-append path wake it. The gate
// is re-evaluated after every wake, so a spurious wake is free.
//
// The loops that are NOT gated are the ones that must fire precisely when
// nothing is happening: keep-alive, leg-liveness, sack, and leg-dataprogress —
// whose sole-leg black-hole path exists to notice a leg that sent bytes and
// delivered none.
package router

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/router/routersettings"
)

// serviceWake is a broadcast wake-up for parked service loops.
//
// Loops park on wait()'s channel; signal() closes it and installs a fresh one,
// releasing every parked loop at once. signal() is called from the data path,
// so it fast-paths on an atomic load and touches the mutex only while a loop is
// actually parked — a group whose loops are all running pays one relaxed load
// per Write and per inbound packet.
type serviceWake struct {
	parked atomic.Int32

	mu sync.Mutex
	ch chan struct{}
}

// wait returns the channel closed by the next signal. Take it BEFORE evaluating
// the gate: a signal racing the evaluation then closes a channel already held,
// so the park sees it immediately instead of sleeping through it.
func (w *serviceWake) wait() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ch == nil {
		w.ch = make(chan struct{})
	}
	return w.ch
}

// signal releases every parked loop. A no-op when none is parked.
func (w *serviceWake) signal() {
	if w.parked.Load() == 0 {
		return
	}
	w.mu.Lock()
	if w.ch != nil {
		close(w.ch)
		w.ch = nil
	}
	w.mu.Unlock()
}

// signalServiceWake releases this group's parked service loops. Called from the
// write path, the inbound-packet path and the leg-append path — everything that
// can turn a gate's answer from dormant to active.
func (rg *RouteGroup) signalServiceWake() {
	rg.wake.signal()
}

// idleSuspendGrace is how long after the last send a group stays awake before
// its traffic-gated loops may park, or 0 when suspension is switched off.
func (rg *RouteGroup) idleSuspendGrace() time.Duration {
	return rg.knDur(routersettings.MuxIdleSuspendGrace)
}

// muxDormant reports that the traffic-gated loops (send-window, tlp,
// reorder-stall) cannot do useful work right now: nothing is outstanding in the
// retransmit buffer, the receive frontier has no gap and nothing is buffered
// behind one, and neither a send nor a receive has happened within the grace
// period.
//
// The retransmit-buffer term is what makes parking safe for send-window: a
// writer parked on a full send window necessarily has data outstanding, so a
// group with an empty retransmit buffer has no writer waiting to be signaled.
// (Parked writers also re-poll on send.window_poll, so a missed wake self-heals
// within milliseconds regardless.)
//
// The RECEIVE term is what keeps the gate from costing more than it saves. A
// group being downloaded to sends almost nothing, so on the send term alone it
// would read as dormant while packets streamed in — and since every arrival
// signals the wake, the loops would park and unpark once per packet. Holding
// them awake for a grace period after the last arrival means a busy group ticks
// exactly as it did before, and only a genuinely quiet one parks.
func (rg *RouteGroup) muxDormant() bool {
	grace := rg.idleSuspendGrace()
	if grace <= 0 {
		return false // suspension disabled
	}
	m := rg.mux
	if m == nil {
		return false
	}
	if m.retxBuf != nil {
		if _, ok := m.retxBuf.MaxSeq(); ok {
			return false // unacked data outstanding
		}
	}
	if m.reorderBuf != nil {
		if m.reorderBuf.Pending() > 0 || m.reorderBuf.GapAge() > 0 {
			return false // an open frontier gap is exactly reorder-stall's job
		}
	}
	if recentlyActive(rg.lastSent.Load(), grace) || recentlyActive(rg.lastRecv.Load(), grace) {
		return false
	}
	return true
}

// recentlyActive reports whether a UnixNano activity stamp falls inside grace.
// A zero stamp means the event has never happened.
func recentlyActive(stamp int64, grace time.Duration) bool {
	return stamp != 0 && time.Since(time.Unix(0, stamp)) < grace
}

// singleLegDormant reports that the leg-gated loops (legstate-resync,
// unidir-flip) cannot do useful work: the group has fewer than two legs, so
// both of their per-leg iterations start past the end of the slice.
func (rg *RouteGroup) singleLegDormant() bool {
	if rg.idleSuspendGrace() <= 0 {
		return false // suspension disabled
	}
	rg.mu.Lock()
	n := len(rg.tps)
	rg.mu.Unlock()
	return n < 2
}
