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

// TestSBDNoParkWithoutTrafficToRuleOn is the live regression for the idle park:
// on mux-legs-2 the detector parked at 01:06:30.615 and re-parked at 01:06:35.615
// with the group carrying nothing, and because a trial cannot fail against a
// pre-park rate of zero the park was permanent — all 15 rows that followed ran
// single-leg, 50 MB at x0.81 and 10 MB at x0.68 where the same route pair with
// parking off gave x1.13. No traffic, no ruling: below the evidence floor the
// demotion is withheld, and the same group carrying real bytes still parks.
func TestSBDNoParkWithoutTrafficToRuleOn(t *testing.T) {
	rg, ids := sbdTrialRig(t)

	// An idle tick: the legs co-vary (they always do when neither is loaded) but
	// nothing is moving, so there is no bottleneck to have detected.
	rg.enforceBottleneckGroups(nil)
	require.False(t, rg.mux.isLegStandby(1), "a park may not be decided on an idle group")
	require.Zero(t, countEvents(rg, MuxEventLegParked))

	// A trickle below the floor is no better: 64 KiB/s is the bar, and a tick
	// carrying half of it over legDataProgressInterval does not clear it.
	rg.enforceBottleneckGroups(deltas(ids, uint64(sbdMinEvidenceRate*legDataProgressInterval/time.Second)/2, 0))
	require.False(t, rg.mux.isLegStandby(1), "a trickle below the evidence floor is not evidence")
	require.Zero(t, countEvents(rg, MuxEventLegParked))

	// Loaded, the very same grouping parks the redundant leg as before — the floor
	// gates when the detector may rule, not what it rules.
	rg.enforceBottleneckGroups(deltas(ids, 25_000_000, 21_500_000))
	require.True(t, rg.mux.isLegStandby(1), "a loaded group is still ruled on")
	require.Equal(t, 1, countEvents(rg, MuxEventLegParked))
}

// TestSBDRefutedParkIsRememberedAfterMirroredPromote is the second half of the
// live defect: the peer mirrors a promote of its own right after a park, so by
// the time the verdict came in the leg was no longer standby — the old code took
// that as "nothing to undo", skipped noteSBDIndependent with it, and the detector
// re-parked the pair on the very next tick. The refutation is a fact about the
// PAIR, so it is recorded either way and only the unpark is skipped.
func TestSBDRefutedParkIsRememberedAfterMirroredPromote(t *testing.T) {
	rg, ids := sbdTrialRig(t)

	rg.enforceBottleneckGroups(deltas(ids, 25_000_000, 21_500_000))
	require.True(t, rg.mux.isLegStandby(1), "precondition: the co-bottlenecked leg is parked on trial")

	// The peer's mirrored leg-state promote lands before the trial is read.
	rg.mux.setLegStandby(1, false)

	// The trial is now ripe and the goodput fell by 27%: the ruling is refuted.
	rg.enforceBottleneckGroups(deltas(ids, 34_000_000, 0))

	require.Equal(t, 1, countEvents(rg, MuxEventParkTrialFailed), "the refutation is recorded even with nothing to unpark")
	require.Zero(t, countEvents(rg, MuxEventLegPromoted), "a leg already active needs no unpark")
	require.True(t, rg.sbdSuppressed(ids[0], ids[1]), "the pair must be marked verified-independent")
	require.False(t, rg.mux.isLegStandby(1), "and the same tick must not re-park it")

	// The backoff holds across further loaded ticks — this is what stops the
	// immediate re-park the live run showed five seconds after the first.
	rg.enforceBottleneckGroups(deltas(ids, 25_000_000, 21_500_000))
	require.False(t, rg.mux.isLegStandby(1), "the suppressed pair stays two pipes for the backoff window")
	require.Equal(t, 1, countEvents(rg, MuxEventLegParked), "no re-park inside the backoff")
}

// TestSBDIdleParkIsNeverPermanent: a park that slipped through with no pre-park
// reading (an older peer's mirror, or one made before the floor was raised on a
// running visor) stays OPEN rather than standing, and is undone on the first
// loaded tick — the A/B is deferred to loaded conditions, not skipped.
func TestSBDIdleParkIsNeverPermanent(t *testing.T) {
	rg, ids := sbdTrialRig(t)

	rg.mux.setLegStandby(1, true)
	rg.beginSBDTrial(ids[1], ids[0], 0) // the idle park, base rate 0

	// Idle ticks do not end it: an ended trial against a base of zero can never
	// fail, which is precisely how the park became permanent.
	for i := 0; i < 3; i++ {
		rg.enforceBottleneckGroups(nil)
	}
	require.True(t, rg.mux.isLegStandby(1), "still parked while there is nothing to judge it by")
	rg.sbdTrialMu.Lock()
	_, open := rg.sbdTrials[ids[1]]
	rg.sbdTrialMu.Unlock()
	require.True(t, open, "the trial must stay open, not end on no evidence")

	// The first loaded tick undoes it and holds the pair apart for one window, so
	// the detector re-rules under load instead of re-parking on the spot.
	rg.enforceBottleneckGroups(deltas(ids, 25_000_000, 21_500_000))
	require.False(t, rg.mux.isLegStandby(1), "an unarbitrable park must not stand once traffic arrives")
	require.Equal(t, 1, countEvents(rg, MuxEventParkTrialFailed))
	require.True(t, rg.sbdSuppressed(ids[0], ids[1]), "the pair is held apart for the deferred A/B")
	require.Equal(t, sbdBackoff, rg.noteSBDIndependent(ids[0], ids[1]),
		"a probation is not a verified-independent window: the backoff does not double off it")
}

// TestSBDMinEvidenceRateKnob: the floor is a live route setting whose default is
// the constant the package compiled with, and zero — the value that let an idle
// park stand for a whole transfer — is refused.
func TestSBDMinEvidenceRateKnob(t *testing.T) {
	require.Equal(t, int64(sbdMinEvidenceRate), SBDMinEvidenceRate())
	require.Equal(t, int64(64<<10), SBDMinEvidenceRate(), "the documented default is 64 KiB/s")

	prev := SBDMinEvidenceRate()
	t.Cleanup(func() { SetSBDMinEvidenceRate(prev) })

	require.True(t, SetSBDMinEvidenceRate(1<<20))
	require.Equal(t, int64(1<<20), SBDMinEvidenceRate())
	require.False(t, SetSBDMinEvidenceRate(0), "a floor of zero is what made an idle park permanent")
	require.False(t, SetSBDMinEvidenceRate(-1), "non-positive is refused")
	require.Equal(t, int64(1<<20), SBDMinEvidenceRate(), "a refused value must not be installed")
}
