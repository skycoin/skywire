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
	sbdDemoteOn(t) // the trial is the arbitration of a PARK, so demotion is on

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

// sbdDemoteOn turns shared-bottleneck DEMOTION on for the duration of one test.
// It is OFF by default (sbdDemoteDefault), so every test about what a park DOES
// has to ask for it; the tests about what a ruling does without it must not.
func sbdDemoteOn(t *testing.T) {
	t.Helper()
	prev := SBDDemote()
	require.True(t, SetSBDDemote(true))
	t.Cleanup(func() { SetSBDDemote(prev) })
}

// creditSendPath makes the group's SEND path look like it delivered vals[i]
// SACK-acknowledged bytes on leg i over the tick about to run — the quantity
// sbdSendDeltas differences and every shared-bottleneck decision is weighed
// against. It rewrites the previous reading too, so one call is a complete tick
// however many came before it.
func creditSendPath(rg *RouteGroup, ids []uuid.UUID, vals ...uint64) {
	rb := rg.mux.retxBuf
	if rb == nil {
		return
	}
	cur := make(map[uuid.UUID]uint64, len(ids))
	rb.mu.Lock()
	for i, id := range ids {
		if i < len(vals) {
			rb.ackedByTp[id] += vals[i]
		}
		cur[id] = rb.ackedByTp[id]
	}
	rb.mu.Unlock()

	rg.sbdTrialMu.Lock()
	defer rg.sbdTrialMu.Unlock()
	if rg.sbdAckedPrev == nil {
		rg.sbdAckedPrev = make(map[uuid.UUID]uint64, len(ids))
	}
	for i, id := range ids {
		v := uint64(0)
		if i < len(vals) {
			v = vals[i]
		}
		rg.sbdAckedPrev[id] = cur[id] - v
	}
}

// sbdTick runs one data-progress tick carrying vals[i] bytes on leg i, on both
// the send path (the evidence) and the recv deltas (the reverse-direction floor).
func sbdTick(rg *RouteGroup, ids []uuid.UUID, vals ...uint64) {
	creditSendPath(rg, ids, vals...)
	rg.enforceBottleneckGroups(deltas(ids, vals...))
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
	sbdTick(rg, ids, 25_000_000, 21_500_000)
	require.True(t, rg.mux.isLegStandby(1), "the co-bottlenecked leg is parked (on trial)")
	require.Equal(t, 1, countEvents(rg, MuxEventLegParked))

	// Tick 2: with leg 1 out the group delivers 6.80 MB/s — a 27% fall, far past
	// the 15% limit, so the park cost real capacity.
	sbdTick(rg, ids, 34_000_000, 0)

	require.False(t, rg.mux.isLegStandby(1), "a park that cost the group goodput must be undone")
	require.Equal(t, 1, countEvents(rg, MuxEventParkTrialFailed), "the failed trial is visible as a mux event")
	require.Equal(t, 1, countEvents(rg, MuxEventLegPromoted), "the unpark is countable against the park")
	require.True(t, rg.sbdSuppressed(ids[0], ids[1]), "the pair is marked verified-independent")
	require.Equal(t, 1, countEvents(rg, MuxEventLegParked), "and the same tick must not re-park it")

	// The backoff doubles on a repeat and never merges the pair while it runs.
	sbdTick(rg, ids, 25_000_000, 21_500_000)
	require.False(t, rg.mux.isLegStandby(1), "the suppressed pair stays two pipes for the backoff window")
	require.Equal(t, 2*sbdBackoff, rg.noteSBDIndependent(ids[0], ids[1]), "a repeat failure doubles the exemption")
}

// TestSBDParkStandsWhenGoodputHolds is the other half: when the legs really were
// one pipe, parking the redundant one costs the group nothing, so the trial ends
// quietly and the park stays — the detector keeps everything it was worth.
func TestSBDParkStandsWhenGoodputHolds(t *testing.T) {
	rg, ids := sbdTrialRig(t)

	sbdTick(rg, ids, 25_000_000, 21_500_000)
	require.True(t, rg.mux.isLegStandby(1))

	// The remaining leg picks up what the parked one was carrying: 9.30 → 9.18
	// MB/s, a 1% dip well inside the limit.
	sbdTick(rg, ids, 45_900_000, 0)

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
	sbdTick(rg, ids, 0, 0)
	require.False(t, rg.mux.isLegStandby(1), "a park may not be decided on an idle group")
	require.Zero(t, countEvents(rg, MuxEventLegParked))

	// A DOWNLOAD's worth of received bytes is not evidence either: the delay
	// samples come from the send path, and at this end that path is idle.
	rg.enforceBottleneckGroups(deltas(ids, 25_000_000, 21_500_000))
	require.False(t, rg.mux.isLegStandby(1), "recv-side traffic is the opposite end of the samples")
	require.Zero(t, countEvents(rg, MuxEventLegParked))

	// A trickle below the floor is no better: 64 KiB/s is the bar, and a tick
	// carrying half of it over legDataProgressInterval does not clear it.
	sbdTick(rg, ids, uint64(sbdMinEvidenceRate*legDataProgressInterval/time.Second)/2, 0)
	require.False(t, rg.mux.isLegStandby(1), "a trickle below the evidence floor is not evidence")
	require.Zero(t, countEvents(rg, MuxEventLegParked))

	// Loaded, the very same grouping parks the redundant leg as before — the floor
	// gates when the detector may rule, not what it rules.
	sbdTick(rg, ids, 25_000_000, 21_500_000)
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

	sbdTick(rg, ids, 25_000_000, 21_500_000)
	require.True(t, rg.mux.isLegStandby(1), "precondition: the co-bottlenecked leg is parked on trial")

	// The peer's mirrored leg-state promote lands before the trial is read.
	rg.mux.setLegStandby(1, false)

	// The trial is now ripe and the goodput fell by 27%: the ruling is refuted.
	sbdTick(rg, ids, 34_000_000, 0)

	require.Equal(t, 1, countEvents(rg, MuxEventParkTrialFailed), "the refutation is recorded even with nothing to unpark")
	require.Zero(t, countEvents(rg, MuxEventLegPromoted), "a leg already active needs no unpark")
	require.True(t, rg.sbdSuppressed(ids[0], ids[1]), "the pair must be marked verified-independent")
	require.False(t, rg.mux.isLegStandby(1), "and the same tick must not re-park it")

	// The backoff holds across further loaded ticks — this is what stops the
	// immediate re-park the live run showed five seconds after the first.
	sbdTick(rg, ids, 25_000_000, 21_500_000)
	require.False(t, rg.mux.isLegStandby(1), "the suppressed pair stays two pipes for the backoff window")
	require.Equal(t, 1, countEvents(rg, MuxEventLegParked), "no re-park inside the backoff")
}

// TestSBDTrialHasNoVerdictOnAnIdleTick is the other half of the evidence floor:
// two of the four compose-set parks on 2026-09-17 were "refuted" on a tick that
// carried 2 B/s — the gap between two bench rows — which says nothing about the
// legs. A trial reads no verdict below the floor: it stays OPEN until the group
// is carrying something again, and is then arbitrated properly.
func TestSBDTrialHasNoVerdictOnAnIdleTick(t *testing.T) {
	rg, ids := sbdTrialRig(t)

	sbdTick(rg, ids, 25_000_000, 21_500_000)
	require.True(t, rg.mux.isLegStandby(1), "precondition: the co-bottlenecked leg is parked on trial")

	// Three ticks between bench rows: nothing is moving, so nothing is decided.
	for i := 0; i < 3; i++ {
		sbdTick(rg, ids, 10, 0)
	}
	require.True(t, rg.mux.isLegStandby(1), "an idle tick may not end a trial either way")
	require.Zero(t, countEvents(rg, MuxEventParkTrialFailed), "2 B/s is not a refutation")
	rg.sbdTrialMu.Lock()
	_, open := rg.sbdTrials[ids[1]]
	rg.sbdTrialMu.Unlock()
	require.True(t, open, "the trial must stay open, not end on no evidence")

	// The next loaded tick is the real A/B, and here the park cost 27%.
	sbdTick(rg, ids, 34_000_000, 0)
	require.False(t, rg.mux.isLegStandby(1), "a park refuted under load is undone")
	require.Equal(t, 1, countEvents(rg, MuxEventParkTrialFailed))
}

// TestSBDRulingIsRecordedNotActedOnByDefault is what ships: with sbd_demote off
// (the default) the detector still rules and still hands the grouping to the mux,
// but the leg stays in the active set and the ruling is recorded as an sbd_ruling
// mux event carrying the numbers behind it. The rig measured the detector 0-for-8,
// so this is the difference between a diagnosis and a demotion.
func TestSBDRulingIsRecordedNotActedOnByDefault(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 2)
	rg.muxEvents = &muxEventRing{}
	require.False(t, SBDDemote(), "demotion must be off unless a test asks for it")

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
	ids := []uuid.UUID{mts[0].Entry.ID, mts[1].Entry.ID}

	sbdTick(rg, ids, 25_000_000, 21_500_000)

	require.False(t, rg.mux.isLegStandby(1), "a ruling must not park a leg with demotion off")
	require.Zero(t, countEvents(rg, MuxEventLegParked), "and must not emit a park event")
	require.Equal(t, 1, countEvents(rg, MuxEventSBDRuling), "the ruling itself is recorded")

	// The GROUPING still reaches the mux — that half only costs a weight, and it
	// is what makes rebuildWeights count one bottleneck as one unit of capacity.
	rg.mux.legMu.Lock()
	g0, g1 := rg.mux.groupOf(0), rg.mux.groupOf(1)
	rg.mux.legMu.Unlock()
	require.Equal(t, g0, g1, "the two legs must still be grouped as one bottleneck")

	var reason string
	for _, e := range rg.muxEvents.snapshot() {
		if e.Event == MuxEventSBDRuling {
			reason = e.Reason
		}
	}
	require.Contains(t, reason, "leg 1", "the ruling names the leg it would have parked")
	require.Contains(t, reason, "skew", "and the correlation numbers behind it")
	require.Contains(t, reason, "B/s", "and the rate it was read at")

	// Nothing stands as a trial either: there is no park to arbitrate.
	rg.sbdTrialMu.Lock()
	n := len(rg.sbdTrials)
	rg.sbdTrialMu.Unlock()
	require.Zero(t, n, "a ruling that parked nothing opens no trial")
}

// TestSBDDemoteKnob: the gate is a live route setting, off by default, and it
// round-trips both ways.
func TestSBDDemoteKnob(t *testing.T) {
	require.Equal(t, sbdDemoteDefault, SBDDemote())
	require.False(t, SBDDemote(), "the measured default is OFF")

	prev := SBDDemote()
	t.Cleanup(func() { SetSBDDemote(prev) })

	require.True(t, SetSBDDemote(true))
	require.True(t, SBDDemote())
	require.True(t, SetSBDDemote(false), "unlike the numeric knobs there is no value to refuse")
	require.False(t, SBDDemote())
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
