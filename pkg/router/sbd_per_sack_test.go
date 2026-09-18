// Package router pkg/router/sbd_per_sack_test.go c2-net-routing
//
// Coverage for the two halves of the mux-compose-T2xL2 finding: shared-bottleneck
// detection folding a delay sample per SACK (so a verdict lands in seconds, not
// the ~110s the 30s liveness pong needs to clear sbdMinSamples), and RACK judging
// a frame by the delay of the leg it actually rode.
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// sbdSACKRig is a two-leg group whose mux feeds its per-SACK delay samples into
// the SBD windows, with the fold rate limiter effectively off so a test can
// deliver its samples without wall-clock sleeps.
func sbdSACKRig(t *testing.T) (*RouteGroup, []uuid.UUID) {
	t.Helper()
	rg, mts, _ := createMuxRouteGroup(t, 2)
	rg.muxEvents = &muxEventRing{} // capture the park event the verdict emits
	rg.mux.onLegDelaySample = rg.foldLegDelaySample
	sbdDemoteOn(t) // these cases are about what a ruling DOES
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

	sbdTick(rg, ids, 5_000_000, 5_000_000)
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

	sbdTick(rg, ids, 5_000_000, 5_000_000)
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

// TestRackDoesNotJudgeSlowLegByFastLegDelay is the B regression: on the measured
// composition the two pinned legs left over near first hops, so tp.GetLatency and
// the ecfRttMs built on it read ~the fast leg for BOTH, and a leg whose ROUTE
// measures 410 ms had its in-flight frames declared lost at 151 ms × 1.25. The
// end-to-end pong latency is now the leg's own delay basis, so its frames wait
// for its own delay and only the fast leg's hole is retransmitted.
func TestRackDoesNotJudgeSlowLegByFastLegDelay(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("sbd-test")
	m := newRouteMux(log, true)
	setLegRTTs(m, []float64{151, 151}) // both legs look 151 ms from the near edge
	fast, slow := uuid.New(), uuid.New()
	m.setLegE2ERTT(fast, 151)
	m.setLegE2ERTT(slow, 410) // ...but the slow leg's ROUTE is 410 ms

	group := m.rackThreshold()
	require.InDelta(t, float64(151*1.25*float64(time.Millisecond)), float64(group), float64(time.Millisecond),
		"group-wide threshold is the slowest ecfRttMs × the reorder factor")
	require.GreaterOrEqual(t, m.rackThresholdFor(slow), 512*time.Millisecond,
		"the slow leg's holes must wait for the slow leg's own delay")
	require.Equal(t, group, m.rackThresholdFor(fast), "a leg at the group basis keeps the group threshold")

	// Seq 7 rode the slow leg, seq 8 the fast one, both sent 300 ms ago — past
	// the group threshold (189 ms), well inside the slow leg's own (512 ms).
	for seq, tp := range map[uint32]uuid.UUID{7: slow, 8: fast} {
		m.retxBuf.Store(seq, []byte("f"), tp)
		m.retxBuf.mu.Lock()
		m.retxBuf.entries[seq].sentAt = time.Now().Add(-300 * time.Millisecond)
		m.retxBuf.mu.Unlock()
	}
	got := m.onSACKReceived(6, []uint64{0b100}, 0, false) // bit 2 = seq 9 received; 7 and 8 missing
	require.Equal(t, []uint32{8}, got, "only the fast leg's hole is overdue; the 410 ms leg's frame is in ordinary flight")
}

// TestLegLatencyByTpUsesEndToEnd: the proactive HoL path's per-leg overdueness
// gate reads the same end-to-end basis, so a frame on a slow-routed leg is not
// nudged onto the fastest leg one near-hop RTT into its ordinary flight.
func TestLegLatencyByTpUsesEndToEnd(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 2)
	mts[0].SetLatency(20)
	mts[1].SetLatency(20)
	rg.mux.setLegE2ERTT(mts[1].Entry.ID, 410)

	got := rg.mux.legLatencyByTp(mts)
	require.InDelta(t, 20, got[mts[0].Entry.ID], 0.001, "a leg with no pong sample keeps its first-hop RTT")
	require.InDelta(t, 410, got[mts[1].Entry.ID], 0.001, "a slow-routed leg is judged on its whole route")
}

// legDelaySamples drives one SACK through a retx buffer and returns the per-leg
// delay samples it produced, keyed by transport.
func legDelaySamples(rb *retxBuffer, lastContig uint32, words []uint64) map[uuid.UUID][]float64 {
	got := map[uuid.UUID][]float64{}
	rb.onAckDelayTp = func(tp uuid.UUID, d time.Duration) {
		got[tp] = append(got[tp], float64(d)/float64(time.Millisecond))
	}
	rb.ProcessSACK(lastContig, words, time.Second)
	return got
}

// TestSACKSamplesOnlyNewlyAckedFramesPerLeg is the sampling defect itself. A SACK
// purges everything below its contiguous frontier, and the frames behind a stall
// come out in one batch whose ages are all ~the age of the STALL — one number, on
// both legs. Sampling that batch per leg fed the detector two views of the group's
// own head-of-line coupling, which correlates whatever the network is doing (the
// rig measured the detector 0-for-8 on it). Only the frame whose arrival moved the
// frontier, and the bits the bitmap newly reports, are per-leg samples now.
func TestSACKSamplesOnlyNewlyAckedFramesPerLeg(t *testing.T) {
	rb := newRetxBuffer(64)
	legA, legB := uuid.New(), uuid.New()

	// Six frames striped A,B,A,B,A,B, all sent ~400ms ago behind one stalled
	// frontier; seq 6 (leg B) is the one whose arrival advances it.
	for seq := uint32(1); seq <= 6; seq++ {
		tp := legA
		if seq%2 == 0 {
			tp = legB
		}
		rb.Store(seq, []byte("frame"), tp)
	}
	rb.mu.Lock()
	for seq := uint32(1); seq <= 6; seq++ {
		rb.entries[seq].sentAt = time.Now().Add(-400 * time.Millisecond)
	}
	rb.mu.Unlock()

	got := legDelaySamples(rb, 6, nil)
	require.Empty(t, got[legA], "the frames purged behind the frontier are not this leg's round trips")
	require.Len(t, got[legB], 1, "exactly one sample: the frame that moved the frontier")
	require.InDelta(t, 400, got[legB][0], 50)

	// The bitmap half: seqs the SACK newly reports above the frontier ARE each
	// leg's own round trips, and each carries its own age.
	rb2 := newRetxBuffer(64)
	rb2.Store(10, []byte("frame"), legA)
	rb2.Store(11, []byte("frame"), legB)
	rb2.mu.Lock()
	rb2.entries[10].sentAt = time.Now().Add(-120 * time.Millisecond)
	rb2.entries[11].sentAt = time.Now().Add(-320 * time.Millisecond)
	rb2.mu.Unlock()

	got2 := legDelaySamples(rb2, 9, []uint64{0b11}) // seqs 10 and 11 received
	require.Len(t, got2[legA], 1)
	require.Len(t, got2[legB], 1)
	require.InDelta(t, 120, got2[legA][0], 50, "each leg is sampled on its own frame's age")
	require.InDelta(t, 320, got2[legB][0], 50)
}

// TestIndependentLegSeriesAreNotRuledShared is the consequence: fed two
// INDEPENDENT round-trip series the detector leaves the legs alone, and fed the
// SAME series (which is what the frontier-batch sampling manufactured) it rules
// them one pipe. The old sampling could only ever produce the second case.
func TestIndependentLegSeriesAreNotRuledShared(t *testing.T) {
	independent := func(t *testing.T) *RouteGroup {
		t.Helper()
		rg, ids := sbdSACKRig(t)
		a := []float64{50, 140, 50, 140, 50, 140, 50, 140} // a congested queue
		b := []float64{90, 90.5, 90, 90.5, 90, 90.5, 90, 90.5}
		for i := range a {
			rg.mux.recordAckDelayTp(ids[0], time.Duration(a[i]*float64(time.Millisecond)))
			rg.mux.recordAckDelayTp(ids[1], time.Duration(b[i]*float64(time.Millisecond)))
		}
		sbdTick(rg, ids, 5_000_000, 5_000_000)
		return rg
	}
	rg := independent(t)
	require.False(t, rg.mux.isLegStandby(1), "two independent series are two pipes")
	require.Zero(t, countEvents(rg, MuxEventLegParked))
	require.Zero(t, countEvents(rg, MuxEventSBDRuling), "and there is no ruling to record either")

	rg2, ids2 := sbdSACKRig(t)
	same := []float64{50, 140, 50, 140, 50, 140, 50, 140}
	for i := range same {
		rg2.mux.recordAckDelayTp(ids2[0], time.Duration(same[i]*float64(time.Millisecond)))
		rg2.mux.recordAckDelayTp(ids2[1], time.Duration(same[i]*float64(time.Millisecond)))
	}
	sbdTick(rg2, ids2, 5_000_000, 5_000_000)
	require.True(t, rg2.mux.isLegStandby(1), "one series on both legs is the shared-bottleneck signature")
}
