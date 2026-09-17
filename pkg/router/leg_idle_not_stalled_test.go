// Package router pkg/router/leg_idle_not_stalled_test.go c2-net-routing
package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
)

// openStuckGap backdates the reorder frontier's gap so gapAge() reads older than
// legDataStallGapAge — the receiver is genuinely stuck on a missing sequence.
func openStuckGap(rg *RouteGroup, age time.Duration) {
	rb := rg.mux.reorderBuf
	rb.mu.Lock()
	rb.gapSince = time.Now().Add(-age)
	rb.mu.Unlock()
}

// TestIdleLegIsNotStalledLeg drives the RECEIVE-side data-progress detector with
// the live shape that produced the defect: a pinned two-leg set where the PEER
// stripes an upload onto one leg only, so the other delivers ~nothing.
//
// Measured on a 10 MB upload to a DE exit: the exit saw 9537 / 17 / 16 B on the
// idle leg over three consecutive samples and parked it with "reorder gap age 0s,
// stuck=false". That park mirrored back over CapLegState, and the five 50 MB
// downloads that followed ran on ONE leg (50.0-50.4 MB vs 1 byte).
//
// With the frontier HEALTHY there is no evidence the leg lost anything, so it
// must not be parked and must not post an event. With the frontier STUCK the
// pre-existing park (#4964) still fires.
func TestIdleLegIsNotStalledLeg(t *testing.T) {
	rg, _, _ := createMuxRouteGroup(t, 2)
	rg.muxEvents = &muxEventRing{}
	// createMuxRouteGroup leaves standbyNewLegs false → manual (pinned) mode, the
	// mode the live exit was in when it parked the idle leg.
	require.False(t, rg.standbyNewLegs)

	// Seed the per-leg recv snapshot so the next pass computes real deltas.
	rg.legDataProgressServiceFn(0)

	// One sample of a busy leg 0 and a silent leg 1, frontier healthy.
	rg.mux.recordPayload(0, 8<<20)
	require.Less(t, rg.mux.gapAge(), legDataStallGapAge, "frontier must be healthy for this leg of the test")
	rg.legDataProgressServiceFn(0)

	require.False(t, rg.mux.isLegStandby(1),
		"an idle leg (the peer simply did not send on it) must not be parked while the reorder frontier is healthy")
	require.Zero(t, countEvents(rg, MuxEventLegParked), "no park, no park event")

	// Same silence, but now the receiver IS stuck behind a missing sequence: the
	// leg is genuinely stalling the group, so the park stands.
	openStuckGap(rg, legDataStallGapAge+2*time.Second)
	rg.mux.recordPayload(0, 8<<20)
	rg.legDataProgressServiceFn(0)

	require.True(t, rg.mux.isLegStandby(1),
		"a leg delivering nothing while the frontier is STUCK past legDataStallGapAge is still parked")
	require.Equal(t, 1, countEvents(rg, MuxEventLegParked), "the stuck-frontier park posts its event")
}

// TestPeerLegStateIsAnEvent covers the second half of the defect: the peer's
// CapLegState park was adopted silently, so a leg that sat parked for five
// minutes left 11 mux events and ZERO parks in the log — the one-leg downloads
// were unattributable. Every adopted transition must post an event with by=peer,
// and only a real transition (the peer re-broadcasts its whole set every
// legStateResyncInterval).
func TestPeerLegStateIsAnEvent(t *testing.T) {
	rg, _, _ := createMuxRouteGroup(t, 2)
	rg.muxEvents = &muxEventRing{}
	rg.mux.legStateEnabled = true

	rg.mu.Lock()
	rid := rg.rvs[1].KeyRouteID()
	rg.mu.Unlock()

	require.NoError(t, rg.handleLegStatePacket(routing.MakeLegStatePacket(rid, true)))
	require.True(t, rg.mux.isLegStandby(1), "the peer's park is mirrored onto this side's mux")
	require.True(t, rg.legParkedByPeer(1), "the park is recorded as PEER-originated")

	parks := legEventsBy(rg, MuxEventLegParked, MuxByPeer)
	require.Len(t, parks, 1, "the adopted park posts exactly one event")
	require.Equal(t, 1, parks[0].LegIndex)
	require.Contains(t, parks[0].Reason, "peer-mirrored park")

	// The peer's periodic resync repeats the same state — not a transition.
	require.NoError(t, rg.handleLegStatePacket(routing.MakeLegStatePacket(rid, true)))
	require.Len(t, legEventsBy(rg, MuxEventLegParked, MuxByPeer), 1,
		"a repeated leg-state packet is not a new event")

	require.NoError(t, rg.handleLegStatePacket(routing.MakeLegStatePacket(rid, false)))
	require.False(t, rg.mux.isLegStandby(1), "the peer's promote is mirrored too")
	require.False(t, rg.legParkedByPeer(1), "a peer promote clears the peer-origin record")

	promotes := legEventsBy(rg, MuxEventLegPromoted, MuxByPeer)
	require.Len(t, promotes, 1, "the adopted promote posts its own event")
	require.Contains(t, promotes[0].Reason, "peer-mirrored promote")
}

// legEventsBy returns the recorded mux events of a kind attributed to `by`.
func legEventsBy(rg *RouteGroup, kind, by string) []MuxEvent {
	var out []MuxEvent
	for _, e := range rg.muxEvents.snapshot() {
		if e.Event == kind && e.By == by {
			out = append(out, e)
		}
	}
	return out
}
