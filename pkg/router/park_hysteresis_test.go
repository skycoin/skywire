// Package router pkg/router/park_hysteresis_test.go c2-net-routing
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/transport"
)

// parkFlapRig builds a 3-leg group in which the shared-bottleneck detector sees
// legs 0 and 1 behind ONE pipe (identical OWD-variation signatures) while every
// leg's end-to-end latency sits inside the same tight band. That is exactly the
// live shape of the bench set mux-legs-3: the bottleneck controller wants leg 1
// parked, the latency band sees nothing wrong with it.
func parkFlapRig(t *testing.T) (*RouteGroup, []*transport.ManagedTransport) {
	t.Helper()
	rg, mts, _ := createMuxRouteGroup(t, 3)
	rg.muxEvents = &muxEventRing{} // capture the lifecycle events this rig emits
	sbdDemoteOn(t)                 // this rig is about what a PARK does, so demotion is on

	rg.legLivenessMu.Lock()
	if rg.legE2ELatency == nil {
		rg.legE2ELatency = map[uuid.UUID]float64{}
	}
	if rg.legOWD == nil {
		rg.legOWD = map[uuid.UUID]*sbdWindow{}
	}
	// All three legs are in-band, so the latency band never wants to park any of
	// them — and would happily re-admit one another controller parked.
	rg.legE2ELatency[mts[0].Entry.ID] = 50
	rg.legE2ELatency[mts[1].Entry.ID] = 49
	rg.legE2ELatency[mts[2].Entry.ID] = 52
	// Legs 0 and 1 share a delay-variation signature → one bottleneck group.
	for _, id := range []uuid.UUID{mts[0].Entry.ID, mts[1].Entry.ID} {
		w := newSBDWindow()
		for _, s := range []float64{50, 62, 50, 62, 50, 62, 50, 62} {
			w.push(s)
		}
		rg.legOWD[id] = w
	}
	// Leg 2 has too few samples to be judged → its own singleton group, kept.
	w2 := newSBDWindow()
	w2.push(52)
	rg.legOWD[mts[2].Entry.ID] = w2
	rg.legLivenessMu.Unlock()

	return rg, mts
}

// loadedDeltas is one data-progress tick in which every leg is carrying real
// traffic, so the group clears the shared-bottleneck evidence floor
// (sbdMinEvidenceRateDefault) and the detector is allowed to rule. A ruling made below
// that floor is withheld — see TestSBDNoParkWithoutTrafficToRuleOn. The bytes are
// credited on the SEND path (SACK-acknowledged), which is the side the evidence
// floor is measured on, and returned as the recv deltas the reverse-floor guard
// reads.
func loadedDeltas(rg *RouteGroup, mts []*transport.ManagedTransport) map[uuid.UUID]uint64 {
	ids := make([]uuid.UUID, 0, len(mts))
	vals := make([]uint64, 0, len(mts))
	for _, mt := range mts {
		ids = append(ids, mt.Entry.ID)
		vals = append(vals, 5_000_000)
	}
	creditSendPath(rg, ids, vals...)
	return deltas(ids, vals...)
}

func countEvents(rg *RouteGroup, kind string) int {
	n := 0
	for _, e := range rg.muxEvents.snapshot() {
		if e.Event == kind {
			n++
		}
	}
	return n
}

// TestAdaptiveParkHoldStopsControllerFlap is the regression for the measured
// 5s park/promote loop. enforceBottleneckGroups and enforceLatencyBand run in
// the SAME data-progress tick: the first parks leg 1 as co-bottlenecked with the
// primary, the second finds leg 1 perfectly in-band and (before the hold)
// re-admitted it on the spot. The leg was then active for the next tick, parked
// again, and the pair flapped it once every 5s — 14 leg_parked events for one
// leg over 65s on the live 3-leg bench set, each mirrored to the peer and each
// forcing the demote-time retransmit flush.
//
// With the hold the leg is parked ONCE and stays parked across every subsequent
// tick inside legParkMinHoldDefault.
func TestAdaptiveParkHoldStopsControllerFlap(t *testing.T) {
	rg, mts := parkFlapRig(t)

	// Tick 1: the bottleneck controller parks leg 1...
	rg.enforceBottleneckGroups(loadedDeltas(rg, mts))
	require.True(t, rg.mux.isLegStandby(1), "leg 1 must be parked as co-bottlenecked with the primary")
	reason, held := rg.adaptiveParkHeld(mts[1].Entry.ID)
	require.True(t, held, "the adaptive park must start a hold")
	require.Contains(t, reason, "shared bottleneck", "the hold must carry the reason the controller named")

	// ...and the latency band, later in the same tick, must NOT undo it.
	rg.enforceLatencyBand(nil)
	require.True(t, rg.mux.isLegStandby(1),
		"the latency band re-admitted a leg the bottleneck controller just parked — this is the flap")

	// 13 more ticks inside the hold: still one park, no promotions.
	for i := 0; i < 13; i++ {
		rg.enforceBottleneckGroups(loadedDeltas(rg, mts))
		rg.enforceLatencyBand(nil)
		require.True(t, rg.mux.isLegStandby(1), "leg 1 must stay parked for the whole hold (tick %d)", i+2)
	}
	require.Equal(t, 1, countEvents(rg, MuxEventLegParked),
		"a held leg must be parked once, not re-parked every tick")
	require.Equal(t, 0, countEvents(rg, MuxEventLegPromoted),
		"nothing may promote a leg whose adaptive park is still held")

	// Legs 0 and 2 are untouched: the hold damps decisions, it does not make them.
	require.False(t, rg.mux.isLegStandby(0), "the primary is never parked")
	require.False(t, rg.mux.isLegStandby(2), "a leg in its own bottleneck group stays active")
}

// TestAdaptiveParkHoldExpiresAndPromotes: once the hold expires and the reason
// no longer holds (the leg is back in its own bottleneck group), the adaptive
// promoter re-admits the leg and says so in the event log.
func TestAdaptiveParkHoldExpiresAndPromotes(t *testing.T) {
	rg, mts := parkFlapRig(t)

	rg.enforceBottleneckGroups(loadedDeltas(rg, mts))
	require.True(t, rg.mux.isLegStandby(1), "leg 1 parked")

	// Age the park past its minimum hold.
	rg.adaptiveParkMu.Lock()
	p := rg.adaptiveParks[mts[1].Entry.ID]
	p.at = p.at.Add(-legParkMinHoldDefault - time.Second)
	rg.adaptiveParks[mts[1].Entry.ID] = p
	rg.adaptiveParkMu.Unlock()

	if _, held := rg.adaptiveParkHeld(mts[1].Entry.ID); held {
		t.Fatal("the hold must expire after legParkMinHoldDefault")
	}

	rg.enforceLatencyBand(nil)
	require.False(t, rg.mux.isLegStandby(1), "an expired hold must let the in-band leg back in")
	require.Equal(t, 1, countEvents(rg, MuxEventLegPromoted), "the promotion must be visible in the event log")

	// The record is gone, so the next adaptive park starts a fresh hold.
	_, held := rg.adaptiveParkHeld(mts[1].Entry.ID)
	require.False(t, held)
	rg.enforceBottleneckGroups(loadedDeltas(rg, mts))
	_, held = rg.adaptiveParkHeld(mts[1].Entry.ID)
	require.True(t, held, "the re-park starts a new hold")
}

// TestOperatorPinOverridesParkHold: the hold damps the ADAPTIVE controllers only.
// An operator pinning the leg (mux add / mux set → activatePinnedLeg) promotes it
// immediately, hold or no hold.
func TestOperatorPinOverridesParkHold(t *testing.T) {
	rg, mts := parkFlapRig(t)

	rg.enforceBottleneckGroups(loadedDeltas(rg, mts))
	require.True(t, rg.mux.isLegStandby(1), "leg 1 parked")
	if _, held := rg.adaptiveParkHeld(mts[1].Entry.ID); !held {
		t.Fatal("precondition: the park is held")
	}

	rg.activatePinnedLeg(mts[1].Entry.ID)
	require.False(t, rg.mux.isLegStandby(1), "an operator pin must beat the hold")
	_, held := rg.adaptiveParkHeld(mts[1].Entry.ID)
	require.False(t, held, "the operator pin clears the hold")
	require.Equal(t, 1, countEvents(rg, MuxEventLegPromoted), "the operator promotion is recorded")
}

// TestPromoteLegAdaptiveIgnoresPrimaryAndUnknown covers the guards: the primary
// is never promoted through this seam (it is never parked), and a leg index with
// no transport still mirrors and records rather than panicking.
func TestPromoteLegAdaptiveIgnoresPrimaryAndUnknown(t *testing.T) {
	rg, _ := parkFlapRig(t)

	require.False(t, rg.promoteLegAdaptive(0, "primary"), "leg 0 is not promotable through the adaptive seam")
	require.False(t, rg.promoteLegAdaptive(-1, "negative"), "a negative index is refused")
	require.True(t, rg.promoteLegAdaptive(9, "no such leg"),
		"an index with no transport is a mirror-only promotion, not a panic")
}
