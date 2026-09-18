// Package router pkg/router/route_mux_probe_only_test.go c2-net-routing
package router

import (
	"testing"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// probeOnlyMux builds an ACCEPTOR-side directional mux with two MULTIHOP legs —
// the exit's download shape — and rules leg 1 probe-only against leg 0 with the
// delay bases given (ms).
func probeOnlyMux(t *testing.T, fastMs, slowMs float64) (*routeMux, []*transport.ManagedTransport, []routing.Rule) {
	t.Helper()
	dst, src, hopA, hopB, _ := endpointsForTest(t)

	// Acceptor: this end sends the DOWNLOAD, which rides the multihop class.
	m := newRouteMux(logging.MustGetLogger("probe-only-test"), true)
	m.setDirectional(false, dst, src)
	if m.forwardSender() {
		t.Fatal("acceptor must not be the forward sender — the gate under test is on the download path")
	}

	tps := []*transport.ManagedTransport{{}, {}}
	tps[0].Entry.ID = uuid.New()
	tps[1].Entry.ID = uuid.New()
	setRemoteForTest(tps[0], hopA)
	setRemoteForTest(tps[1], hopB)
	fwd := make([]routing.Rule, 2)

	m.growLegs(2)
	m.markLegReady(0)
	m.markLegReady(1)

	// The schedule selectByDirection's tier 1 reads: an unweighted round-robin
	// over the live legs, which is what Rebuild builds for every predictive mode.
	m.tpSelector.SetMode(WeightModeECF)
	m.tpSelector.Rebuild(tps)

	states := []ecfLegState{
		{rttMs: fastMs, ready: true},
		{rttMs: slowMs, ready: true},
	}
	m.legMu.Lock()
	rulings := m.ruleProbeOnlyLegsLocked(states)
	m.legMu.Unlock()
	m.reportProbeRulings(tps, rulings)
	return m, tps, fwd
}

// download runs n frames of frameBytes through the reverse-direction pick,
// charging each to the leg that took it exactly as RouteGroup.write does, and
// returns the bytes each leg carried.
func download(t *testing.T, m *routeMux, tps []*transport.ManagedTransport, fwd []routing.Rule, n, frameBytes int) map[int]uint64 {
	t.Helper()
	dst, src, _, _, _ := endpointsForTest(t)
	carried := map[int]uint64{}
	for i := 0; i < n; i++ {
		_, _, idx, ok := m.selectByDirection(tps, fwd, false, dst, src)
		if !ok {
			t.Fatalf("frame %d: selectByDirection returned ok=false with two ready reverse legs", i)
		}
		nb := uint64(frameBytes) //nolint:gosec // test-local frame size
		carried[idx] += nb
		m.recordSent(idx, nb)
	}
	return carried
}

// TestOutclassedLegKeptToProbeBudget is the regression guard for the 2026-09-18
// composition collapse (bench/2026-09-18/1008cc8e5/mux-compose-T2xL2): group
// rg49220 paired a 7.4 MB/s leg with one running at ~60 KB/s, and because the
// reverse direction's tier 1 is an unweighted round-robin the exit put 0.5-2.2
// MB of every 10 MB download on the slow leg — the five trials took 8.3-37.4 s,
// each one's duration the bytes placed there divided by its rate. The group's
// own detector had the number: "mean 755.9 vs 9291.3 ms over 8/8 samples".
//
// With the gate, a leg that far behind carries at most one probe budget per
// window; the rest of the transfer goes to the leg that can take it.
func TestOutclassedLegKeptToProbeBudget(t *testing.T) {
	m, tps, fwd := probeOnlyMux(t, 755.9, 9291.3)

	const frame = 16 * 1024
	const frames = 1024 // 16 MB, more than any of the failing 10 MB trials
	carried := download(t, m, tps, fwd, frames, frame)

	budget := uint64(LegProbeBytes()) //nolint:gosec // the default is positive
	// One frame may overshoot: the budget is charged after the send, so the
	// frame that crosses the line is already on the wire.
	limit := budget + frame
	if carried[1] > limit {
		t.Errorf("outclassed leg carried %d bytes, more than the probe budget %d (+1 frame): a 12x-slower leg is still taking a proportional share",
			carried[1], limit)
	}
	if carried[1] == 0 {
		t.Error("outclassed leg carried nothing — the probe must keep measuring it so the ruling can lift")
	}
	if carried[0] == 0 {
		t.Fatal("the healthy leg carried nothing")
	}
	if total := carried[0] + carried[1]; total != uint64(frames*frame) {
		t.Errorf("legs carried %d bytes of %d offered", total, frames*frame)
	}
}

// TestHealthySkewKeepsFullShare pins the default: the leg pairs the rig
// actually runs — 44 ms against 166 ms by first hop, 172.7 vs 214.6 ms and
// 336.3 vs 513.9 ms by delay basis (ratios 3.8, 1.24, 1.53) — are NOT cut, so
// the gate changes nothing about a group that aggregates today.
func TestHealthySkewKeepsFullShare(t *testing.T) {
	for _, c := range []struct{ fast, slow float64 }{
		{44, 166},      // the legs-2 pinned pair, by first hop
		{172.7, 214.6}, // an sbd_ruling from the same campaign
		{336.3, 513.9}, // and the widest one it recorded
		{20, 200},      // ratio 10, but both under the absolute floor
	} {
		m, tps, fwd := probeOnlyMux(t, c.fast, c.slow)
		if m.legProbeExhausted(1) {
			t.Errorf("%.1f ms against %.1f ms: leg 1 was cut before carrying a byte", c.slow, c.fast)
		}
		carried := download(t, m, tps, fwd, 400, 16*1024)
		if carried[0] == 0 || carried[1] == 0 {
			t.Errorf("%.1f ms against %.1f ms: the download must still spread across both legs; got %v", c.slow, c.fast, carried)
		}
	}
}

// TestProbeRulingIsReportedAndLifts proves the ruling is announced both ways and
// that it lifts on its own once the leg's basis comes back — the leg is never
// parked, so nothing has to un-park it.
func TestProbeRulingIsReportedAndLifts(t *testing.T) {
	m, tps, _ := probeOnlyMux(t, 200, 5000)

	var kinds []bool
	m.SetLegProbeRulingFn(func(idx, _ int, _ *transport.ManagedTransport, probeOnly bool, reason string) {
		if idx != 1 {
			t.Errorf("ruling named leg %d, want leg 1", idx)
		}
		if reason == "" {
			t.Error("ruling carried no reason")
		}
		kinds = append(kinds, probeOnly)
	})

	// Re-rule with the same numbers: no transition, so nothing is reported.
	states := []ecfLegState{{rttMs: 200, ready: true}, {rttMs: 5000, ready: true}}
	m.legMu.Lock()
	rulings := m.ruleProbeOnlyLegsLocked(states)
	m.legMu.Unlock()
	m.reportProbeRulings(tps, rulings)
	if len(kinds) != 0 {
		t.Fatalf("a re-ruling with unchanged bases reported %d event(s)", len(kinds))
	}

	// The leg recovers: the basis falls back within the ratio.
	states[1].rttMs = 260
	m.legMu.Lock()
	rulings = m.ruleProbeOnlyLegsLocked(states)
	m.legMu.Unlock()
	m.reportProbeRulings(tps, rulings)
	if len(kinds) != 1 || kinds[0] {
		t.Fatalf("recovery should report exactly one full-share event; got %v", kinds)
	}
	if m.legProbeExhausted(1) {
		t.Error("a leg restored to a full share must not be gated")
	}
}

// TestProbeGateOffWhenRatioDisabled proves the knob turns the whole gate off.
func TestProbeGateOffWhenRatioDisabled(t *testing.T) {
	prev := LegStarveRatio()
	t.Cleanup(func() { SetLegStarveRatio(prev) })
	if !SetLegStarveRatio(1000) {
		t.Fatal("SetLegStarveRatio(1000) refused")
	}
	if SetLegStarveRatio(1) || SetLegStarveRatio(0.5) {
		t.Error("a ratio at or below 1 must be refused")
	}
	if SetLegProbeBytes(0) || SetLegProbeBytes(-1) {
		t.Error("a non-positive probe budget must be refused")
	}

	m, tps, fwd := probeOnlyMux(t, 755.9, 9291.3)
	if m.legProbeExhausted(1) {
		t.Fatal("no leg may be gated with the ratio set past any real skew")
	}
	carried := download(t, m, tps, fwd, 400, 16*1024)
	if carried[1] == 0 {
		t.Error("with the gate off the download spreads as it did before")
	}
}
