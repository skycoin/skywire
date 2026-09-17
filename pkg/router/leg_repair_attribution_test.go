// Package router pkg/router/leg_repair_attribution_test.go
//
// Two per-leg fairness rules a multi-leg sender must hold, both learned from a
// live two-leg exit that ran 7.5 MB/s where two independent groups on the same
// paths did 10.66:
//
//  1. The writer parks only when no ready leg is both un-shed and holding
//     window room, and the spill always names a leg with ROOM — preferring an
//     un-shed one — so a frame never lands in a full window and never idles
//     while a healthy leg could take it. Dropping the shed from the gate
//     entirely was measured worse, not better: 86 % of a 50 MB download landed
//     on the bloated leg (44.1 vs 7.1 MB) at 5.86 MB/s against 7.62 MB/s
//     braked, and parks per download ROSE from 12.4 to 22.
//  2. A repair is charged to the leg that LOST the frame, not to the leg that
//     volunteered to carry it. Repairs ride the fastest live leg, so the old
//     carrier-charging made the healthiest leg look the lossiest (1813
//     retransmits against the fast leg vs 9 on the bloated one) and the
//     coupled controller demoted it to standby for a whole download phase.
package router

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSendWindowShedLegIsTheOnlyRoom is case (a), the shape the live campaign
// produced: the healthy leg is out of window and the only headroom left belongs
// to a congestion-shed leg (500 ms against a 100 ms baseline). The writer PARKS
// — briefly, bounded by sendWindowWaitMax and released by the first SACK that
// frees the healthy leg — rather than pour the frame into the queue that is
// already stalling the reorder frontier.
//
// This is the one case where "never park while headroom exists" and "never feed
// a congesting leg" disagree, and the measurement decides it: the braked build
// parked here and ran 7.62 MB/s on 50 MB downloads, migrating the transfer
// 70/30 -> 26/74 off the bloated leg across trials; the build that sent to the
// shed leg instead put 44.1 MB against 7.1 MB on it (86/14) and ran 5.86 MB/s,
// and its parking ROSE (12.4 -> 22 waits per download) because both windows
// then filled. The spill still never idles when a leg is genuinely usable —
// see cases (b) and (c).
func TestSendWindowShedLegIsTheOnlyRoom(t *testing.T) {
	ts := newTransportSelector()
	ts.SetMode(WeightModeECF)
	ts.SetECFState([]ecfLegState{
		{rttMs: 154, rttMinMs: 154, rateBps: 1e6, cwndBytes: ecfMinWindowBytes, ready: true},
		{rttMs: 500, rttMinMs: 100, rateBps: 1e7, cwndBytes: ecfMaxWindowBytes, ready: true},
	})
	ts.SetInflight([]int64{ecfMinWindowBytes, 1 << 20})

	require.True(t, ts.Saturated(0), "leg 0 is out of window")
	require.True(t, ts.Saturated(1), "the RTT shed marks leg 1 for the PICK")
	require.True(t, ts.AllReadySaturated(),
		"no leg is both un-shed and has room: the writer parks (bounded, SACK-released)")
	// The spill still names leg 1: if the caller sends anyway — the park times
	// out after sendWindowWaitMax — the frame goes where there is room, never
	// onto the leg that has none.
	require.Equal(t, 1, ts.FirstUnsaturated(),
		"the only leg with room is the fallback target even though it is shed")

	// The moment the healthy leg's window frees, the park lifts and the un-shed
	// leg takes the frame — the migration the brake exists to produce.
	ts.SetInflight([]int64{ecfMinWindowBytes / 2, 1 << 20})
	require.False(t, ts.AllReadySaturated(), "an un-shed leg with room releases the writer")
	require.Equal(t, 0, ts.FirstUnsaturated(), "and wins the spill over the shed leg")
}

// TestSendWindowSpillPrefersUnshedLeg is case (b): both legs are shed and both
// have room. No leg is both un-shed and has room, so the gate holds the writer
// for the bounded wait; the spill target is still the first leg WITH room, so a
// timed-out wait never sends into a full window. Once one leg is un-shed it
// wins the spill regardless of index and the park lifts — the brake that
// migrates load OFF a congesting leg.
func TestSendWindowSpillPrefersUnshedLeg(t *testing.T) {
	ts := newTransportSelector()
	ts.SetMode(WeightModeECF)
	ts.SetECFState([]ecfLegState{
		{rttMs: 600, rttMinMs: 100, rateBps: 1e6, cwndBytes: ecfMaxWindowBytes, ready: true},
		{rttMs: 500, rttMinMs: 100, rateBps: 1e7, cwndBytes: ecfMaxWindowBytes, ready: true},
	})
	ts.SetInflight([]int64{1 << 20, 1 << 20})
	require.True(t, ts.AllReadySaturated(),
		"every leg is shed: the writer parks briefly rather than deepen a queue")
	require.Equal(t, 0, ts.FirstUnsaturated(), "all shed: the first leg with room takes it")

	// Leg 1's RTT returns to its baseline: it is now the only un-shed leg with
	// room and must win the spill over the lower-indexed shed leg.
	ts.SetECFState([]ecfLegState{
		{rttMs: 600, rttMinMs: 100, rateBps: 1e6, cwndBytes: ecfMaxWindowBytes, ready: true},
		{rttMs: 120, rttMinMs: 100, rateBps: 1e7, cwndBytes: ecfMaxWindowBytes, ready: true},
	})
	require.Equal(t, 1, ts.FirstUnsaturated(), "an un-shed leg with room beats a shed one")
	require.False(t, ts.AllReadySaturated())
}

// TestSendWindowParksWhenNoLegHasRoom is case (c): every ready leg is at its
// window, so no frame could go out anywhere and parking is free. A SACK that
// frees half of one window releases the writer immediately, even though that
// leg's RTT is still inflated.
func TestSendWindowParksWhenNoLegHasRoom(t *testing.T) {
	ts := newTransportSelector()
	ts.SetMode(WeightModeECF)
	ts.SetECFState([]ecfLegState{
		{rttMs: 154, rttMinMs: 154, rateBps: 1e6, cwndBytes: ecfMinWindowBytes, ready: true},
		{rttMs: 500, rttMinMs: 100, rateBps: 1e7, cwndBytes: ecfMaxWindowBytes, ready: true},
	})
	ts.SetInflight([]int64{ecfMinWindowBytes, ecfMaxWindowBytes})
	require.True(t, ts.AllReadySaturated(), "every ready leg full: the writer parks")
	require.Equal(t, -1, ts.FirstUnsaturated(), "no headroom anywhere: no spill target")

	// A SACK frees half of the un-shed leg 0: it is now both un-shed and holding
	// room, so the park lifts at once and it wins the spill.
	ts.SetInflight([]int64{ecfMinWindowBytes / 2, ecfMaxWindowBytes})
	require.False(t, ts.AllReadySaturated(), "freed window must release the writer")
	require.Equal(t, 0, ts.FirstUnsaturated())
}

// TestSendWindowLoneLegNeverParks keeps #4962's rule: a group with one ready
// leg has nowhere to shed to, so its writer never parks even when the lone leg
// is well past its window — the transport's own backpressure bounds it.
func TestSendWindowLoneLegNeverParks(t *testing.T) {
	ts := newTransportSelector()
	ts.SetMode(WeightModeECF)
	ts.SetECFState([]ecfLegState{
		{rttMs: 400, rttMinMs: 100, rateBps: 1e6, cwndBytes: ecfMinWindowBytes, ready: true},
		{rttMs: 30, rttMinMs: 30, rateBps: 1e6, cwndBytes: ecfMinWindowBytes, ready: false}, // standby
	})
	ts.SetInflight([]int64{10 * ecfMinWindowBytes, 0})
	require.False(t, ts.AllReadySaturated(), "a lone ready leg must never park the writer")
}

// TestRepairChargedToTheLegThatLostTheFrame pins rule (2): 1000 repairs of leg
// 0's frames, all carried by the fast leg 1, leave leg 1's loss signal at zero
// and put every one of them on leg 0 — so the coupled controller's loss ratio
// (retransmits per sent segment, see policy/preset tickCoupled) sheds the leg
// that actually lost frames, not the one that healed them.
func TestRepairChargedToTheLegThatLostTheFrame(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 2)
	rg.mux.sackEnabled = true

	// Leg 1 is the fastest live leg, so every repair is selected onto it.
	mts[0].SetLatency(400)
	mts[1].SetLatency(20)

	const n = 1000
	seqs := make([]uint32, 0, n)
	for i := 0; i < n; i++ {
		seq := uint32(i) //nolint:gosec
		// Every sequence was originally sent on leg 0 — leg 0 is where the gap is.
		rg.mux.retxBuf.Store(seq, []byte(fmt.Sprintf("payload-%d", i)), mts[0].Entry.ID)
		seqs = append(seqs, seq)
	}

	require.NoError(t, rg.resendSeqs(seqs))

	require.EqualValues(t, n, rg.mux.retransmitsAt(0),
		"the leg whose frames were lost must carry the whole loss signal")
	require.EqualValues(t, 0, rg.mux.retransmitsAt(1),
		"the leg that carried the repairs must not be charged for them")

	// The bytes still belong to the carrier: they really went out on leg 1, and
	// that is also the denominator of the loss ratio.
	snap := rg.mux.snapshotLegs()
	require.Greater(t, snap[1].SentBytes, uint64(0), "the carrier is charged the repair BYTES")
	require.EqualValues(t, 0, snap[0].SentBytes, "the lossy leg sent nothing on this path")

	// The demotion input: what the rotation engine sees per leg.
	rg.mu.Lock()
	legs := rg.snapshotLegs()
	rg.mu.Unlock()
	require.Len(t, legs, 2)
	require.EqualValues(t, n, legs[0].Retransmits)
	require.EqualValues(t, 0, legs[1].Retransmits,
		"a healthy carrier must stay demotion-free no matter how many repairs it relays")

	// A repair re-tags the sequence to the leg it now rides, so if the SAME
	// frame goes missing again the gap is genuinely leg 1's and leg 1 is
	// charged. Attribution follows the gap, not the history.
	require.NoError(t, rg.resendSeqs(seqs[:10]))
	require.EqualValues(t, n, rg.mux.retransmitsAt(0), "leg 0 did not lose these frames the second time")
	require.EqualValues(t, 10, rg.mux.retransmitsAt(1), "the second loss is leg 1's own")
}

// TestMuxStatsCarriesWindowAndInflight pins the per-leg send-window telemetry
// the exit-side snapshot is read from: `visor state --select mux_route_groups`
// must show each leg's ECF window (cwnd) and its real unacknowledged bytes, so
// "which leg was the writer waiting on" is answerable from one capture instead
// of inferred from goodput. MuxLeg.WindowBytes/InflightBytes carry them through
// to MuxLegInfo's window_bytes / inflight_bytes JSON.
func TestMuxStatsCarriesWindowAndInflight(t *testing.T) {
	rg, _, _ := createMuxRouteGroup(t, 2)
	rg.mux.tpSelector.SetMode(WeightModeECF)
	rg.mux.tpSelector.SetECFState([]ecfLegState{
		{rttMs: 154, rttMinMs: 154, rateBps: 1e6, cwndBytes: ecfMinWindowBytes, ready: true},
		{rttMs: 100, rttMinMs: 100, rateBps: 1e7, cwndBytes: ecfMaxWindowBytes, ready: true},
	})
	rg.mux.tpSelector.SetInflight([]int64{ecfMinWindowBytes, 1 << 20})

	legs := rg.MuxStats().Legs
	require.Len(t, legs, 2)
	require.EqualValues(t, ecfMinWindowBytes, legs[0].WindowBytes)
	require.EqualValues(t, ecfMinWindowBytes, legs[0].InflightBytes)
	require.EqualValues(t, ecfMaxWindowBytes, legs[1].WindowBytes)
	require.EqualValues(t, 1<<20, legs[1].InflightBytes)
}
