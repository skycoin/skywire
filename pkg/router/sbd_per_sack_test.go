// Package router pkg/router/sbd_per_sack_test.go c2-net-routing
//
// Coverage for the per-SACK half of the mux-compose-T2xL2 finding: shared-bottleneck
// detection folds a delay sample per SACK, so a verdict lands in seconds instead of
// the ~110s the 30s liveness pong needs to clear sbdMinSamples.
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// sbdSACKRig is a two-leg group whose mux feeds its per-SACK delay samples into
// the SBD windows, with the fold rate limiter effectively off so a test can
// deliver its samples without wall-clock sleeps.
func sbdSACKRig(t *testing.T) (*RouteGroup, []uuid.UUID) {
	t.Helper()
	rg, mts, _ := createMuxRouteGroup(t, 2)
	rg.muxEvents = &muxEventRing{} // capture the park event the verdict emits
	rg.mux.onLegDelaySample = rg.foldLegDelaySample
	prev := SBDSampleInterval()
	require.True(t, SetSBDSampleInterval(time.Nanosecond))
	t.Cleanup(func() { SetSBDSampleInterval(prev) })
	return rg, []uuid.UUID{mts[0].Entry.ID, mts[1].Entry.ID}
}

// TestSBDRulesWithinAFewSACKs: two legs behind ONE uplink show the same
// delay-variation signature in their SACK feedback, so the detector groups them
// and parks the redundant one after a handful of SACKs — the same park reason as
// the pong-fed path, reached in samples rather than in minutes.
func TestSBDRulesWithinAFewSACKs(t *testing.T) {
	rg, ids := sbdSACKRig(t)

	// Correlated: both legs queue behind the same buffer, so both oscillate
	// between the same base and spike levels (different base delays on purpose —
	// the CV test is scale-invariant, which is the point of RFC 8382's statistics).
	leg0 := []float64{50, 62, 50, 62, 50, 62, 50, 62}
	leg1 := []float64{80, 99, 80, 99, 80, 99, 80, 99}
	sacks := 0
	for i := range leg0 {
		rg.mux.recordAckDelayTp(ids[0], time.Duration(leg0[i]*float64(time.Millisecond)))
		rg.mux.recordAckDelayTp(ids[1], time.Duration(leg1[i]*float64(time.Millisecond)))
		sacks++
	}
	require.LessOrEqual(t, sacks, sbdWindowSamples, "a verdict must not need more SACKs than the window holds")

	rg.enforceBottleneckGroups(nil)
	require.True(t, rg.mux.isLegStandby(1), "co-bottlenecked leg must be parked from the per-SACK samples alone")
	require.False(t, rg.mux.isLegStandby(0), "the primary is never parked")
	require.Equal(t, 1, countEvents(rg, MuxEventLegParked), "the park must emit the same single leg_parked event")
}

// TestSBDDoesNotParkUncorrelatedLegs: two legs whose delay series have unrelated
// shapes are NOT merged, so neither is parked — the conservative half of the rule
// (never collapse a leg's capacity on a guess) survives the faster cadence.
func TestSBDDoesNotParkUncorrelatedLegs(t *testing.T) {
	rg, ids := sbdSACKRig(t)

	// Leg 0 oscillates hard (a shared queue); leg 1 is all but flat (its own
	// uncongested pipe): the CV test separates them.
	leg0 := []float64{50, 140, 50, 140, 50, 140, 50, 140}
	leg1 := []float64{90, 90.5, 90, 90.5, 90, 90.5, 90, 90.5}
	for i := range leg0 {
		rg.mux.recordAckDelayTp(ids[0], time.Duration(leg0[i]*float64(time.Millisecond)))
		rg.mux.recordAckDelayTp(ids[1], time.Duration(leg1[i]*float64(time.Millisecond)))
	}

	rg.enforceBottleneckGroups(nil)
	require.False(t, rg.mux.isLegStandby(0))
	require.False(t, rg.mux.isLegStandby(1), "legs with unrelated delay signatures are two pipes, not one")
	require.Zero(t, countEvents(rg, MuxEventLegParked))
}

// TestSBDSampleIntervalRateLimitsFolds: a SACK burst must not overwrite a leg's
// whole window with one queue state, so at the default interval only the first
// of a back-to-back run is folded.
func TestSBDSampleIntervalRateLimitsFolds(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 2)
	rg.mux.onLegDelaySample = rg.foldLegDelaySample
	require.Equal(t, sbdSampleInterval, SBDSampleInterval(), "default must be the compiled-in constant")

	id := mts[0].Entry.ID
	for i := 0; i < 20; i++ {
		rg.mux.recordAckDelayTp(id, 40*time.Millisecond)
	}
	rg.legLivenessMu.Lock()
	n := len(rg.legOWD[id].samples())
	rg.legLivenessMu.Unlock()
	require.Equal(t, 1, n, "a burst inside one SBDSampleInterval folds exactly one sample")
}

// TestSBDMinSamplesKnob: the sample floor is a live knob, and raising it past
// what the legs have collected withholds the verdict (its today-default is the
// constant the package compiled with).
func TestSBDMinSamplesKnob(t *testing.T) {
	require.Equal(t, sbdMinSamples, SBDMinSamples())
	prev := SBDMinSamples()
	t.Cleanup(func() { SetSBDMinSamples(prev) })

	corr := computeSBDStats([]float64{50, 62, 50, 62, 50, 62})
	require.True(t, sbdSimilar(corr, corr))
	require.True(t, SetSBDMinSamples(12))
	require.False(t, sbdSimilar(corr, corr), "six samples must not satisfy a floor of twelve")
	require.False(t, SetSBDMinSamples(0), "non-positive is refused")
	require.False(t, SetSBDSampleInterval(0), "non-positive is refused")
}
