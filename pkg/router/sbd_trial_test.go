// Package router pkg/router/sbd_trial_test.go c2-net-routing
//
// Coverage for the goodput arbitration of a shared-bottleneck park. The live
// case this pins: two legs over distinct intermediates and distinct first hops
// that the detector ruled co-bottlenecked, costing 50 MB downloads 9.30 MB/s
// (x1.13 paired) → 6.80 MB/s (x0.86) when the park landed. Goodput says the
// ruling was wrong; the trial is what lets the code say so too.
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// sbdTrialRig is a two-leg group whose legs share a delay-variation signature —
// so the detector parks leg 1 on the first ruling — with the trial window pulled
// to a nanosecond so the verdict is ripe on the next tick without a sleep.
func sbdTrialRig(t *testing.T) (*RouteGroup, []uuid.UUID) {
	t.Helper()
	rg, mts, _ := createMuxRouteGroup(t, 2)
	rg.muxEvents = &muxEventRing{}

	rg.legLivenessMu.Lock()
	for _, mt := range mts {
		w := newSBDWindow()
		for _, s := range []float64{50, 62, 50, 62, 50, 62, 50, 62} {
			w.push(s)
		}
		rg.legOWD[mt.Entry.ID] = w
		rg.legE2ELatency[mt.Entry.ID] = 50
	}
	rg.legLivenessMu.Unlock()

	prev := SBDTrialWindow()
	require.True(t, SetSBDTrialWindow(time.Nanosecond))
	t.Cleanup(func() { SetSBDTrialWindow(prev) })

	return rg, []uuid.UUID{mts[0].Entry.ID, mts[1].Entry.ID}
}

// deltas builds one data-progress tick's per-leg recv deltas.
func deltas(ids []uuid.UUID, vals ...uint64) map[uuid.UUID]uint64 {
	out := make(map[uuid.UUID]uint64, len(ids))
	for i, id := range ids {
		if i < len(vals) {
			out[id] = vals[i]
		}
	}
	return out
}

// TestSBDParkTrialUndoneWhenGoodputDrops is the live regression: the park stands
// only as long as the group keeps delivering. Here the two legs were carrying
// independent capacity, so parking one costs the group a third of its aggregate
// rate (the measured 9.30 → 6.80 MB/s, in the same proportion) — the trial fails,
// the leg is unparked inside the trial window, and the pair is exempt from
// further shared-bottleneck rulings for the backoff window.
func TestSBDParkTrialUndoneWhenGoodputDrops(t *testing.T) {
	rg, ids := sbdTrialRig(t)

	// Tick 1: 9.30 MB/s aggregate across both legs → the detector parks leg 1 and
	// records that rate as the bar its trial must clear.
	rg.enforceBottleneckGroups(deltas(ids, 25_000_000, 21_500_000))
	require.True(t, rg.mux.isLegStandby(1), "the co-bottlenecked leg is parked (on trial)")
	require.Equal(t, 1, countEvents(rg, MuxEventLegParked))

	// Tick 2: with leg 1 out the group delivers 6.80 MB/s — a 27% fall, far past
	// the 15% limit, so the park cost real capacity.
	rg.enforceBottleneckGroups(deltas(ids, 34_000_000, 0))

	require.False(t, rg.mux.isLegStandby(1), "a park that cost the group goodput must be undone")
	require.Equal(t, 1, countEvents(rg, MuxEventParkTrialFailed), "the failed trial is visible as a mux event")
	require.Equal(t, 1, countEvents(rg, MuxEventLegPromoted), "the unpark is countable against the park")
	require.True(t, rg.sbdSuppressed(ids[0], ids[1]), "the pair is marked verified-independent")
	require.Equal(t, 1, countEvents(rg, MuxEventLegParked), "and the same tick must not re-park it")

	// The backoff doubles on a repeat and never merges the pair while it runs.
	rg.enforceBottleneckGroups(deltas(ids, 25_000_000, 21_500_000))
	require.False(t, rg.mux.isLegStandby(1), "the suppressed pair stays two pipes for the backoff window")
	require.Equal(t, 2*sbdBackoff, rg.noteSBDIndependent(ids[0], ids[1]), "a repeat failure doubles the exemption")
}

// TestSBDParkStandsWhenGoodputHolds is the other half: when the legs really were
// one pipe, parking the redundant one costs the group nothing, so the trial ends
// quietly and the park stays — the detector keeps everything it was worth.
func TestSBDParkStandsWhenGoodputHolds(t *testing.T) {
	rg, ids := sbdTrialRig(t)

	rg.enforceBottleneckGroups(deltas(ids, 25_000_000, 21_500_000))
	require.True(t, rg.mux.isLegStandby(1))

	// The remaining leg picks up what the parked one was carrying: 9.30 → 9.18
	// MB/s, a 1% dip well inside the limit.
	rg.enforceBottleneckGroups(deltas(ids, 45_900_000, 0))

	require.True(t, rg.mux.isLegStandby(1), "a park that cost no goodput stands")
	require.Zero(t, countEvents(rg, MuxEventParkTrialFailed))
	require.Zero(t, countEvents(rg, MuxEventLegPromoted))
	require.False(t, rg.sbdSuppressed(ids[0], ids[1]), "an upheld park proves nothing about independence")

	// An idle group is no evidence either way: a park that lands while nothing is
	// moving can never fail its trial.
	require.False(t, sbdTrialFailed(0, 0, SBDTrialLoss()), "an idle trial cannot fail")
}

// TestSBDTrialKnobs: the three terms of the trial are live route settings whose
// defaults are the constants the package compiled with, and each refuses a value
// that would make the trial meaningless.
func TestSBDTrialKnobs(t *testing.T) {
	require.Equal(t, sbdTrialWindow, SBDTrialWindow())
	require.Equal(t, sbdTrialLoss, SBDTrialLoss())
	require.Equal(t, sbdBackoff, SBDBackoff())

	pw, pl, pb := SBDTrialWindow(), SBDTrialLoss(), SBDBackoff()
	t.Cleanup(func() { SetSBDTrialWindow(pw); SetSBDTrialLoss(pl); SetSBDBackoff(pb) })

	require.True(t, SetSBDTrialWindow(7*time.Second))
	require.Equal(t, 7*time.Second, SBDTrialWindow())
	require.True(t, SetSBDTrialLoss(0.4))
	require.Equal(t, 0.4, SBDTrialLoss())
	require.False(t, sbdTrialFailed(100, 70, SBDTrialLoss()), "a 30% fall is inside a 40% limit")
	require.True(t, SetSBDBackoff(time.Minute))
	require.Equal(t, time.Minute, SBDBackoff())
	require.Equal(t, time.Minute, nextSBDBackoff(0), "the first exemption is the knob's value")
	require.Equal(t, sbdBackoffMax, nextSBDBackoff(sbdBackoffMax), "the doubling is capped")

	require.False(t, SetSBDTrialWindow(0), "non-positive is refused")
	require.False(t, SetSBDTrialLoss(0), "a zero loss limit would undo every park on noise")
	require.False(t, SetSBDTrialLoss(1), "a limit of 1 could never be reached")
	require.False(t, SetSBDBackoff(-time.Second), "non-positive is refused")
}
