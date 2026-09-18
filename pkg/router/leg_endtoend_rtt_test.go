package router

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestRefreshLegWindowsUsesEndToEndFeedbackDelay reproduces the measured live
// defect: two pinned legs whose FIRST-HOP latencies differ by 9.5x (10 ms via
// Amsterdam, 95 ms via Atlanta from the Frankfurt exit) but whose real
// send→ack feedback delay is ~170 ms on BOTH.
//
// On the first-hop basis ECF's hold-back rule (n*rttF < hyst*(rttS+d)) needed
// n >= 95/10 = 9.5 — nine and a half frames queued on the fast leg — before it
// would spill a single frame to the second leg, which is the measured 41.5/9.5
// MB split of a 50 MB download. On the end-to-end basis both legs read 170 ms,
// so the second frame spills.
//
// RACK's threshold is checked on the same rig: it must never presume loss
// inside one real feedback delay (rackReorderFactorDefault x 170 ms).
func TestRefreshLegWindowsUsesEndToEndFeedbackDelay(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 2)

	// First-hop transport RTTs: a near leg and a far leg.
	mts[0].SetLatency(10)
	mts[1].SetLatency(95)
	// ...but the measured end-to-end feedback delay is the same on both.
	rg.mux.recordAckDelayTp(mts[0].Entry.ID, 170*time.Millisecond)
	rg.mux.recordAckDelayTp(mts[1].Entry.ID, 170*time.Millisecond)

	rg.mux.refreshLegWindows(mts)

	states := ecfStatesOf(rg.mux)
	require.Len(t, states, 2)
	for i, st := range states {
		require.InDelta(t, 170.0, st.rttMs, 1.0,
			"leg %d rttMs must be the end-to-end feedback delay, not the first hop", i)
		require.InDelta(t, 170.0, st.rttMinMs, 1.0,
			"leg %d rttMinMs must be the minimum of the SAME end-to-end basis", i)
	}

	// The RACK / TLP reordering-window basis reads the same field.
	require.InDelta(t, 170.0, rg.mux.maxActiveLegRTTms(), 1.0,
		"maxActiveLegRTTms must be the end-to-end feedback delay")

	// ECF's pick: the fast leg is saturated with exactly one window of backlog
	// (n = 1 + inflight/cwnd = 2 queued frames' worth), the second leg is idle.
	const cwnd = 64 * 1024
	legs := []ecfLegState{
		{rttMs: states[0].rttMs, rttMinMs: states[0].rttMinMs, jitterMs: states[0].jitterMs,
			cwndBytes: cwnd, inflightBytes: cwnd, ready: true},
		{rttMs: states[1].rttMs, rttMinMs: states[1].rttMinMs, jitterMs: states[1].jitterMs,
			cwndBytes: cwnd, inflightBytes: 0, ready: true},
	}
	require.Equal(t, 1, ecfPick(legs, false, nil),
		"with both legs at the same end-to-end delay the second queued frame spills to leg 1; "+
			"on the first-hop basis (10 vs 95 ms) it would have held on leg 0 until n >= 9.5")

	// Nothing spills at n = 1 (no backlog at all beyond the window): the fast
	// leg still wins its own race, so the fix does not make the pick trigger-happy.
	legs[0].inflightBytes = 0
	require.Equal(t, 0, ecfPick(legs, false, nil), "an unsaturated fast leg keeps the frame")

	// RACK must not presume loss inside one real feedback delay on either leg.
	// rackReorderFactorDefault x 170 ms = 212.5 ms; the threshold is computed in whole
	// milliseconds, so the bound is the floor of that.
	wantRack := time.Duration(math.Floor(rackReorderFactorDefault*170)) * time.Millisecond
	for _, tp := range mts {
		require.GreaterOrEqual(t, rg.mux.rackThresholdFor(tp.Entry.ID), wantRack,
			"per-leg RACK threshold must be at least %v (rackReorderFactorDefault x the 170ms feedback delay)", wantRack)
	}
}

// ecfStatesOf reads the per-leg ECF snapshot the mux last handed the selector.
func ecfStatesOf(m *routeMux) []ecfLegState {
	m.tpSelector.mu.Lock()
	defer m.tpSelector.mu.Unlock()
	out := make([]ecfLegState, len(m.tpSelector.ecfLegs))
	copy(out, m.tpSelector.ecfLegs)
	return out
}

// TestLatencyBandJudgesOnWindowedMinRTT covers the second half: the band must
// judge a leg on its MINIMUM end-to-end RTT over the last legRTTMinWindow, not
// on the latest (queue-inflated) sample.
//
// Leg 3 swings 40 / 900 / 40 / 800 ms — the live signature of an in-band
// liveness pong queued behind bulk data. Its min over the window is 40 ms,
// level with the other three legs, so it must NOT be parked. A leg whose
// samples are ALL >= 800 ms has no small sample to hide behind and is parked.
func TestLatencyBandJudgesOnWindowedMinRTT(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 4)

	pushSamples := func(id uuid.UUID, samples ...float64) {
		rg.legLivenessMu.Lock()
		defer rg.legLivenessMu.Unlock()
		w := rg.legRTTWin[id]
		if w == nil {
			w = &legRTTWindow{}
			rg.legRTTWin[id] = w
		}
		now := time.Now()
		for i, s := range samples {
			// Spread the samples over the window, newest last.
			w.push(s, now.Add(-time.Duration(len(samples)-1-i)*time.Second))
		}
		// The EWMA the OLD code judged on: leg 3's is ~600ms, out of band.
		rg.legE2ELatency[id] = samples[len(samples)-1]
	}

	for _, i := range []int{0, 1, 2} {
		pushSamples(mts[i].Entry.ID, 40, 42, 38, 41)
	}
	// Busy leg: real latency 40ms, self-inflicted queuing on the other samples.
	pushSamples(mts[3].Entry.ID, 40, 900, 40, 800)

	rg.enforceLatencyBand(nil)

	for i := 0; i < 4; i++ {
		require.False(t, rg.mux.isLegStandby(i),
			"leg %d must stay active: its min-RTT over the window is level with the cluster", i)
	}

	// Now the leg is genuinely slow — every sample in the window is >= 800ms, so
	// the minimum is out of band too and the leg is parked.
	rg.legLivenessMu.Lock()
	rg.legRTTWin[mts[3].Entry.ID] = &legRTTWindow{}
	rg.legLivenessMu.Unlock()
	pushSamples(mts[3].Entry.ID, 900, 820, 800, 950)

	rg.enforceLatencyBand(nil)
	require.True(t, rg.mux.isLegStandby(3),
		"a leg whose WHOLE window is >= 800ms beside a 40ms cluster must be parked")

	// The event names the statistic the decision was taken on.
	require.Equal(t, "min-RTT 800 ms over 30s", bandStatLabel(800, true))
	require.Equal(t, "800 ms", bandStatLabel(800, false))
}

// TestLegRTTWindowMinAndEviction covers the window itself: the minimum ignores
// load spikes, and samples older than legRTTMinWindow fall out.
func TestLegRTTWindowMinAndEviction(t *testing.T) {
	now := time.Now()
	w := &legRTTWindow{}
	require.Zero(t, w.minMs(now), "an empty window has no statistic")

	for i, s := range []float64{40, 900, 40, 800} {
		w.push(s, now.Add(-time.Duration(3-i)*time.Second))
	}
	require.Equal(t, 40.0, w.minMs(now), "the minimum strips the queuing delay out")

	// A sample older than the window is evicted on the next push and never
	// counted, so a leg that WAS fast long ago cannot mask being slow now.
	w2 := &legRTTWindow{}
	w2.push(40, now.Add(-legRTTMinWindow-time.Second))
	w2.push(800, now)
	require.Equal(t, 800.0, w2.minMs(now), "the stale 40ms sample must have fallen out")

	// Non-positive samples are ignored.
	w2.push(0, now)
	w2.push(-1, now)
	require.Equal(t, 800.0, w2.minMs(now))
}
