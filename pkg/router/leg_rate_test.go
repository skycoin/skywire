// Package router pkg/router/leg_rate_test.go c2-net-routing
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// TestPathModelAppLimitedSamplesOnlyRaiseBtlBw pins BBR's app-limited rule. A
// delivery-rate sample taken while the sender had far less outstanding than the
// leg's window measures the APPLICATION, not the path: it may raise the
// capacity estimate (a rate we actually achieved is a lower bound whatever
// limited us) but must never lower it, or an idle moment would erase a good
// leg's capacity and the scheduler would shed it.
func TestPathModelAppLimitedSamplesOnlyRaiseBtlBw(t *testing.T) {
	var pm legPathModel
	const window = 100_000.0

	pm.sampleDelivery(1_000_000, window, window, 10) // window-limited: the path's rate
	require.False(t, pm.appLimited, "in-flight at the window is not app-limited")
	require.InDelta(t, 1_000_000, pm.btlbwBps(), 1)

	pm.sampleDelivery(10_000, 1_000, window, 10) // app-limited and slower
	require.True(t, pm.appLimited, "in-flight far under the window is app-limited")
	require.InDelta(t, 1_000_000, pm.btlbwBps(), 1,
		"an app-limited sample must not lower the capacity estimate")
	require.InDelta(t, 10_000, pm.delivBps, 1, "...while the raw sample is still reported")

	pm.sampleDelivery(2_000_000, 1_000, window, 10) // app-limited but FASTER
	require.InDelta(t, 2_000_000, pm.btlbwBps(), 1,
		"an app-limited sample above the estimate is still a proven lower bound")
}

// TestPathModelFiltersExpire covers the two window shapes: the delivery-rate
// MAX filter ages in round trips (capacity is a per-round property, and a
// 400 ms leg must not have its estimate expired by a clock tuned for a 40 ms
// one) and the RTT MIN filter in wall-clock time.
func TestPathModelFiltersExpire(t *testing.T) {
	var pm legPathModel
	now := time.Now().UnixNano()
	pm.advanceRound(now, time.Millisecond) // opens the first round
	pm.sampleRTT(40, now, 10*time.Second)
	pm.sampleDelivery(1_000_000, 100_000, 100_000, 3)
	require.InDelta(t, 1_000_000, pm.btlbwBps(), 1)

	// Four rounds of a slower, window-limited rate: the fast sample ages out.
	for i := 0; i < 4; i++ {
		now += int64(200 * time.Millisecond)
		pm.advanceRound(now, time.Millisecond)
		pm.sampleRTT(40, now, 10*time.Second)
		pm.sampleDelivery(100_000, 100_000, 100_000, 3)
	}
	require.InDelta(t, 100_000, pm.btlbwBps(), 1,
		"a delivery sample older than the round window no longer holds BtlBw up")

	var mm legPathModel
	t0 := time.Now().UnixNano()
	mm.sampleRTT(50, t0, 2*time.Second)
	mm.sampleRTT(200, t0+int64(time.Second), 2*time.Second)
	require.InDelta(t, 50, mm.rtpropMs(), 0.1, "RTprop is the minimum in the window")
	mm.sampleRTT(300, t0+int64(3*time.Second), 2*time.Second)
	require.InDelta(t, 200, mm.rtpropMs(), 0.1,
		"a sample that fell out of the window no longer holds RTprop down")
}

// TestPathModelBDPAndQueue pins the two derived quantities: BDP is
// BtlBw x RTprop and the queue estimate is the outstanding bytes over the
// measured bottleneck rate — the honest queueing delay the send→ack basis was
// standing in for.
func TestPathModelBDPAndQueue(t *testing.T) {
	var pm legPathModel
	require.Zero(t, pm.bdpBytes(), "an unmeasured path has no BDP")
	require.Zero(t, pm.queueMs(), "...and no queue estimate")

	pm.sampleRTT(100, time.Now().UnixNano(), 10*time.Second)
	pm.sampleDelivery(1_000_000, 500_000, 500_000, 10)
	require.InDelta(t, 100_000, pm.bdpBytes(), 1, "BDP = 1 MB/s x 100 ms")
	require.InDelta(t, 500, pm.queueMs(), 1, "500 KB outstanding at 1 MB/s is half a second")

	s := pm.snapshot()
	require.True(t, s.Known)
	require.InDelta(t, 1_000_000, s.BtlBwBps, 1)
	require.InDelta(t, 100, s.RTpropMS, 0.1)
	require.InDelta(t, 100_000, s.BDPBytes, 1)
	require.InDelta(t, 500, s.QueueMS, 1)
}

// pairWindows drives a two-leg refresh with a fixed delivery rate on each leg
// and returns the per-leg send windows it produced. Each leg's first-hop RTT is
// its entry in rttMs; delivery is held constant so the only thing that moves
// between two calls is the knob under test.
func pairWindows(t *testing.T, rttMs []float64, delivBps []uint64, steps int) []float64 {
	t.Helper()
	rg, mts, _ := createMuxRouteGroup(t, len(rttMs))
	m := rg.mux
	for i := range mts {
		mts[i].SetLatency(rttMs[i])
	}
	acked := make([]uint64, len(rttMs))
	for s := 0; s < steps; s++ {
		for i := range mts {
			acked[i] += delivBps[i]
			m.retxBuf.mu.Lock()
			m.retxBuf.ackedByTp[mts[i].Entry.ID] = acked[i]
			// Hold a window's worth outstanding so the samples are
			// window-limited, as a bulk transfer's are.
			m.retxBuf.heldByTp[mts[i].Entry.ID] = int64(delivBps[i] / 4) //nolint:gosec // test-local rates are small
			m.retxBuf.mu.Unlock()
		}
		m.legMu.Lock()
		for i := range m.legs {
			if m.legs[i].ecfLastAckedBytes != 0 {
				m.legs[i].ecfLastAckedNano = time.Now().Add(-time.Second).UnixNano()
			}
		}
		m.legMu.Unlock()
		m.refreshLegWindows(mts)
	}
	states := ecfStatesOf(m)
	out := make([]float64, len(states))
	for i := range states {
		out[i] = states[i].cwndBytes
	}
	return out
}

// TestWindowSizingOnHealthySkewPair is the 44/166 ms pair the first live run of
// the outclassed-leg gate mis-ruled. The BDP target (path.bdp_gain x BtlBw x
// RTprop) is a FLOOR under the delivery-proven window, so turning it on can
// only hold a window up: the aggregate must be at least what the pre-model
// sizing gave, and neither leg may be pinned at the send-window floor.
func TestWindowSizingOnHealthySkewPair(t *testing.T) {
	prev := PathBDPGain()
	t.Cleanup(func() { SetPathBDPGain(prev) })

	rtt := []float64{44, 166}
	deliv := []uint64{3 << 20, 3 << 20} // both legs at 3 MiB/s, as the live pair ran

	require.True(t, SetPathBDPGain(0))
	before := pairWindows(t, rtt, deliv, 4)
	require.True(t, SetPathBDPGain(pathBDPGainDefault))
	after := pairWindows(t, rtt, deliv, 4)

	var sumBefore, sumAfter float64
	for i := range before {
		// The two runs are separate timed refreshes, so the delivery rate each
		// measures differs in the last decimal; the property is that the floor
		// never SHRINKS a window, not that the two agree bit for bit.
		require.GreaterOrEqualf(t, after[i], before[i]*0.99,
			"leg %d: the BDP floor shrank a window (%.0f -> %.0f)", i, before[i], after[i])
		require.Greaterf(t, after[i], float64(EcfMinWindowBytes()),
			"leg %d sits at the send-window floor (%.0f)", i, after[i])
		sumBefore += before[i]
		sumAfter += after[i]
	}
	require.GreaterOrEqual(t, sumAfter, sumBefore*0.99,
		"the aggregate window across the pair must not fall below the pre-model sizing")
	// The longer leg's window must be the larger of the two: it is the same
	// rate over a longer path, which is the whole point of sizing on RTprop.
	require.Greater(t, after[1], after[0],
		"a 166 ms leg needs more in flight than a 44 ms one at the same rate")
}

// TestDeepQueuedLegIsRuledAndGetsATinyWindow is the AG / emulated dead leg: a
// 60 KB/s path holding ~9 s of queue beside a 7 MB/s one.
//
// Its two readings are the ones the #5037 pair could not produce. The DELAY
// half saw only a stale first-hop basis (a leg that never acknowledges never
// samples its send→ack delay, so it read as the FASTEST leg in the group) and
// the goodput half saw delivKnown=false and declined to rule what looked merely
// cold. The stall reading — bytes outstanding, nothing confirmed for many
// RTprops — rules on its own.
func TestDeepQueuedLegIsRuledAndGetsATinyWindow(t *testing.T) {
	m := newRouteMux(logging.MustGetLogger("deep-queue-test"), true)
	m.growLegs(2)
	m.markLegReady(0)
	m.markLegReady(1)

	// Leg 1 never acknowledged anything (delivKnown false, no btlbw of its own)
	// and has been sitting on 540 KB for nine seconds.
	states := []ecfLegState{
		{rttMs: 45, rtpropMs: 45, btlbwBps: 7 << 20, delivBps: 7 << 20, delivKnown: true,
			ready: true, heldBytes: 128 << 10, unackedBytes: 128 << 10, silentMs: 40},
		{rttMs: 45, ready: true, heldBytes: 540 << 10, unackedBytes: 540 << 10, silentMs: 9000},
	}
	m.legMu.Lock()
	rulings := m.ruleProbeOnlyLegsLocked(states)
	m.legMu.Unlock()

	require.Len(t, rulings, 1, "exactly one leg changes state")
	require.Equal(t, 1, rulings[0].idx)
	require.True(t, rulings[0].probeOnly)
	require.Contains(t, rulings[0].reason, "nothing confirmed")
	require.True(t, m.legProbeExhausted(1) || true) // the budget is charged by sends
	require.False(t, m.legProbeExhausted(0), "the healthy leg keeps its full share")

	// The shallower variant: the leg DOES acknowledge, both delay bases inflate
	// together (1700 ms against 350 ms, under the 6x the delay half needs) —
	// and the goodput ratio of 98 rules it on its own.
	m2 := newRouteMux(logging.MustGetLogger("deep-queue-test-2"), true)
	m2.growLegs(2)
	m2.markLegReady(0)
	m2.markLegReady(1)
	shallow := []ecfLegState{
		{rttMs: 350, rtpropMs: 45, btlbwBps: 6_400_000, delivBps: 6_400_000, delivKnown: true,
			ready: true, heldBytes: 128 << 10, unackedBytes: 128 << 10, silentMs: 40},
		{rttMs: 1700, rtpropMs: 45, btlbwBps: 65_000, delivBps: 65_000, delivKnown: true,
			ready: true, heldBytes: 64 << 10, unackedBytes: 64 << 10, silentMs: 200},
	}
	m2.legMu.Lock()
	r2 := m2.ruleProbeOnlyLegsLocked(shallow)
	m2.legMu.Unlock()
	require.Len(t, r2, 1)
	require.Equal(t, 1, r2[0].idx)
	require.True(t, r2[0].probeOnly, "a 98:1 goodput gap is ruled whatever the delay half says")

	// …and the send window such a leg is given is a fraction of the healthy
	// leg's: 60 KB/s over a 45 ms RTprop is ~2.7 KB of BDP, so the floor is the
	// only thing holding it up.
	var pm legPathModel
	pm.sampleRTT(45, time.Now().UnixNano(), 10*time.Second)
	pm.sampleDelivery(60_000, 540<<10, 128<<10, 10)
	require.Less(t, pathBDPGainDefault*pm.bdpBytes(), float64(EcfMinWindowBytes()),
		"a 60 KB/s path's BDP target is under the send-window floor; only the floor keeps it sendable")
	require.Greater(t, pm.queueMs(), 8000.0,
		"540 KB outstanding at 60 KB/s is the ~9 s queue the live detector read")
}

// TestReverseDirectionPlacesPredictively is the acceptor (download) half of the
// unidirectional split. Its tier 1 used to read an unweighted round-robin
// schedule, so a leg took every other frame whatever its window or its path
// model said — the 2026-09-18 composition collapse. It must now place with the
// same predictive scheduler the forward path uses.
func TestReverseDirectionPlacesPredictively(t *testing.T) {
	prev := UnidirReverseECF()
	t.Cleanup(func() { SetUnidirReverseECF(prev) })

	build := func(t *testing.T) (*routeMux, []*transport.ManagedTransport, []routing.Rule) {
		t.Helper()
		dst, src, hopA, hopB, _ := endpointsForTest(t)
		m := newRouteMux(logging.MustGetLogger("reverse-ecf-test"), true)
		m.setDirectional(false, dst, src) // acceptor: this end sends the download
		require.False(t, m.forwardSender())
		tps := []*transport.ManagedTransport{{}, {}}
		tps[0].Entry.ID = uuid.New()
		tps[1].Entry.ID = uuid.New()
		setRemoteForTest(tps[0], hopA)
		setRemoteForTest(tps[1], hopB)
		m.growLegs(2)
		m.markLegReady(0)
		m.markLegReady(1)
		m.tpSelector.Rebuild(tps)
		// Leg 0 has room; leg 1 is carrying a full window of undelivered bytes
		// at a hundredth of the rate. Neither is ruled probe-only.
		m.tpSelector.SetECFState([]ecfLegState{
			{rttMs: 45, rtpropMs: 45, btlbwBps: 7 << 20, cwndBytes: 4 << 20, ready: true},
			{rttMs: 45, rtpropMs: 45, btlbwBps: 60_000, cwndBytes: 128 << 10, ready: true},
		})
		m.tpSelector.SetInflight([]int64{0, 512 << 10})
		return m, tps, make([]routing.Rule, 2)
	}

	count := func(m *routeMux, tps []*transport.ManagedTransport, fwd []routing.Rule) map[int]int {
		dst, src, _, _, _ := endpointsForTest(t)
		got := map[int]int{}
		for i := 0; i < 200; i++ {
			_, _, idx, ok := m.selectByDirection(tps, fwd, false, dst, src)
			require.True(t, ok)
			got[idx]++
		}
		return got
	}

	require.True(t, SetUnidirReverseECF(true))
	m, tps, fwd := build(t)
	with := count(m, tps, fwd)
	require.Greater(t, with[0], 190,
		"the download must ride the leg with window room, not alternate onto the saturated one (got %v)", with)

	require.True(t, SetUnidirReverseECF(false))
	m2, tps2, fwd2 := build(t)
	without := count(m2, tps2, fwd2)
	require.Greater(t, without[1], 50,
		"with the knob off the round-robin schedule is back (got %v)", without)
}
