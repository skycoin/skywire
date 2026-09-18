// Package router pkg/router/stalled_leg_mirror_test.go c2-net-routing
package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestStalledLegParkHonorsMirror pins the rule the data-progress park was
// missing: the ACTIVE SET IS THE INITIATOR'S, and an acceptor that honors the
// CapLegState mirror (honorsMirrorActiveSet) must not park a stalled leg of its
// own accord — enforceBottleneckGroups and enforceLatencyBand already return
// early on that condition, demoteStalledLegs did not.
//
// Measured shape (bench/2026-09-16/3535e671b-smoke/mux-compose-T2xL2): seven
// leg_parked events, every one of them a by=peer CapLegState mirror, four of
// them mid-set on rg49176's 201.8 ms leg — the EXIT (the acceptor) ran the
// stalled-leg park and signaled it back, and row 9's 15 s blackout fell inside
// one of those park windows.
//
// The acceptor must instead RECORD the stall (leg_stalled, with the gap age) and
// keep the leg active; the initiator's own stalled-leg policy is unchanged.
func TestStalledLegParkHonorsMirror(t *testing.T) {
	// driveStall runs one stuck-frontier data-progress pass on a two-leg group
	// where leg 1 delivers nothing.
	driveStall := func(t *testing.T, initiator bool) *RouteGroup {
		t.Helper()
		rg, _, _ := createMuxRouteGroup(t, 2)
		rg.muxEvents = &muxEventRing{}
		rg.mux.legStateEnabled = true // CapLegState negotiated on both ends
		rg.initiator = initiator
		require.Equal(t, !initiator, rg.honorsMirrorActiveSet())

		rg.legDataProgressServiceFn(0) // seed the per-leg recv snapshot

		// Leg 0 moves bulk, leg 1 delivers nothing, and the no-skip frontier is
		// genuinely stuck behind a missing sequence — the detector's park case.
		openStuckGap(rg, legDataStallGapAgeDefault+2*time.Second)
		rg.mux.recordPayload(0, 8<<20)
		rg.legDataProgressServiceFn(0)
		return rg
	}

	t.Run("acceptor reports and keeps the leg", func(t *testing.T) {
		rg := driveStall(t, false)

		require.False(t, rg.mux.isLegStandby(1),
			"an acceptor must not park a stalled leg: the active set is the initiator's mirror, and the park is signaled back over CapLegState")
		require.Zero(t, countEvents(rg, MuxEventLegParked), "no park, no park event")

		stalls := legEventsBy(rg, MuxEventLegStalled, MuxByAdaptive)
		require.Len(t, stalls, 1, "the stall is still recorded, so it stays attributable from `visor state`")
		require.Equal(t, 1, stalls[0].LegIndex)
		require.Contains(t, stalls[0].Reason, "reported, not parked")
	})

	t.Run("initiator still parks", func(t *testing.T) {
		rg := driveStall(t, true)

		require.True(t, rg.mux.isLegStandby(1),
			"the initiator owns the active set — its stalled-leg park is unchanged")
		require.Equal(t, 1, countEvents(rg, MuxEventLegParked), "the park posts its event")
		require.Zero(t, countEvents(rg, MuxEventLegStalled), "the initiator parks instead of merely reporting")
	})
}
