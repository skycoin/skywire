// Package router pkg/router/park_hysteresis.go c2-net-routing
package router

import (
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/transport"
)

// Adaptive-park hysteresis.
//
// Two controllers share the data-progress tick (legDataProgressInterval, 5s) and
// disagree about the same leg: enforceBottleneckGroups parks a leg that is
// co-bottlenecked with a kept active leg, and enforceLatencyBand — which runs a
// few microseconds later in the SAME tick and knows nothing about the park —
// re-admits it because its latency is still inside the band. The leg is then
// active again for the next tick, gets parked again, and the pair loops.
//
// Measured on the 3-leg bench set (52e430fff, mux-legs-3): 14 leg_parked events
// for one leg over 65s, one every 5s, each mirrored to the peer (CapLegState)
// and each forcing the demote-time retransmit flush of the in-flight window. The
// upload rows overlapping the flap carried 249-446 retransmitted frames and
// 1.10-1.16 wire/goodput against 43-47 frames and 1.02-1.03 on the rows outside
// it.
//
// The fix is hysteresis: an ADAPTIVE park names a reason and starts a minimum
// hold. While the hold runs no adaptive promoter may re-admit the leg, so the
// two controllers cannot trade it back and forth within a tick. The OPERATOR
// still wins immediately — activatePinnedLeg (mux add / mux set) clears the hold
// — so a pin or width change is never gated on it.

// legParkMinHold is how long an ADAPTIVE park holds before any adaptive promoter
// may re-admit the leg. It must exceed the data-progress cadence by enough that
// the controller that parked the leg gets several ticks of evidence before the
// decision is revisited; six ticks is short enough that a leg whose bottleneck
// really cleared rejoins within half a minute. Not a flag: this is the
// controllers' own damping constant, like legDataStallGapAge.
const legParkMinHold = 30 * time.Second

// adaptivePark is one leg's standing adaptive park: when it happened and the
// reason the controller named for it.
type adaptivePark struct {
	at     time.Time
	reason string
}

// noteAdaptivePark records that an adaptive controller parked the leg riding
// tpID, starting its minimum hold. Called by every adaptive park site
// (shared-bottleneck, latency-band, data-progress stall); an operator park is
// deliberately NOT recorded — only adaptive decisions are damped.
func (rg *RouteGroup) noteAdaptivePark(tpID uuid.UUID, reason string) {
	if tpID == uuid.Nil {
		return
	}
	rg.adaptiveParkMu.Lock()
	defer rg.adaptiveParkMu.Unlock()
	if rg.adaptiveParks == nil {
		rg.adaptiveParks = make(map[uuid.UUID]adaptivePark)
	}
	// Keep the ORIGINAL park time: re-parking a leg that is already held must not
	// extend the hold indefinitely (that would turn a damper into a ratchet).
	if _, held := rg.adaptiveParks[tpID]; held {
		return
	}
	rg.adaptiveParks[tpID] = adaptivePark{at: time.Now(), reason: reason}
}

// adaptiveParkHeld reports whether tpID's adaptive park is still within its
// minimum hold, and the reason that park named.
func (rg *RouteGroup) adaptiveParkHeld(tpID uuid.UUID) (string, bool) {
	if tpID == uuid.Nil {
		return "", false
	}
	rg.adaptiveParkMu.Lock()
	defer rg.adaptiveParkMu.Unlock()
	p, ok := rg.adaptiveParks[tpID]
	if !ok {
		return "", false
	}
	if time.Since(p.at) >= legParkMinHold {
		return p.reason, false
	}
	return p.reason, true
}

// clearAdaptivePark drops tpID's park record: the leg is active again, either
// because an adaptive promoter re-admitted it after the hold or because the
// OPERATOR pinned it (which overrides the hold outright).
func (rg *RouteGroup) clearAdaptivePark(tpID uuid.UUID) {
	if tpID == uuid.Nil {
		return
	}
	rg.adaptiveParkMu.Lock()
	defer rg.adaptiveParkMu.Unlock()
	delete(rg.adaptiveParks, tpID)
}

// legTransportAt returns the transport riding leg idx, or nil. Takes rg.mu.
func (rg *RouteGroup) legTransportAt(idx int) *transport.ManagedTransport {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	if idx < 0 || idx >= len(rg.tps) {
		return nil
	}
	return rg.tps[idx]
}

// promoteLegAdaptive re-admits leg idx to the active set on behalf of an
// ADAPTIVE controller, mirrors the promotion to the peer (CapLegState) and emits
// the lifecycle event with its reason — unless the leg's own adaptive park is
// still inside legParkMinHold, in which case the promotion is refused and the
// leg stays parked. Returns true when the leg was promoted.
//
// Every adaptive promoter goes through here: this is the single seam where the
// park/promote flap is damped, and the single place an adaptive promotion
// becomes visible in the mux event log (it was previously silent, which is why
// the live flap showed only one half of the loop).
func (rg *RouteGroup) promoteLegAdaptive(idx int, reason string) bool {
	if rg.mux == nil || idx <= 0 {
		return false
	}
	tp := rg.legTransportAt(idx)
	var tpID uuid.UUID
	if tp != nil {
		tpID = tp.Entry.ID
	}
	if parkReason, held := rg.adaptiveParkHeld(tpID); held {
		rg.logger.Debugf("park-hysteresis: leg %d stays parked (%s); %v hold not expired, refusing the adaptive re-admit (%s)",
			idx, parkReason, legParkMinHold, reason)
		return false
	}
	rg.mux.setLegStandby(idx, false)
	rg.sendLegState(idx, false)
	rg.clearAdaptivePark(tpID)
	if tp != nil {
		rg.noteLegEvent(MuxEventLegPromoted, reason, MuxByAdaptive, idx, rg.legCount(), tp, nil)
	} else {
		rg.noteMuxEvent(MuxEvent{Event: MuxEventLegPromoted, By: MuxByAdaptive, LegIndex: idx,
			Legs: rg.legCount(), Reason: reason})
	}
	return true
}
