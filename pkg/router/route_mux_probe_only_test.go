// Package router pkg/router/route_mux_probe_only_test.go c2-net-routing
package router

import (
	"testing"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// legReading is one leg as the window refresh hands it to the ruling: its
// end-to-end delay basis in ms and what the peer's SACKs proved it delivered.
type legReading struct {
	basisMs  float64
	delivBps float64
}

// probeOnlyMux builds an ACCEPTOR-side directional mux with two MULTIHOP legs —
// the exit's download shape — and runs the ruling over the readings given.
func probeOnlyMux(t *testing.T, fast, slow legReading) (*routeMux, []*transport.ManagedTransport, []routing.Rule) {
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

	rule(t, m, tps, fast, slow)
	return m, tps, fwd
}

// rule re-runs the ruling over a fresh pair of readings, as a window refresh
// does, and reports the transitions.
func rule(t *testing.T, m *routeMux, tps []*transport.ManagedTransport, fast, slow legReading) {
	t.Helper()
	states := []ecfLegState{
		{rttMs: fast.basisMs, delivBps: fast.delivBps, delivKnown: true, ready: true},
		{rttMs: slow.basisMs, delivBps: slow.delivBps, delivKnown: true, ready: true},
	}
	m.legMu.Lock()
	rulings := m.ruleProbeOnlyLegsLocked(states)
	m.legMu.Unlock()
	m.reportProbeRulings(tps, rulings)
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
// own detector had the delay reading: "mean 755.9 vs 9291.3 ms over 8/8
// samples", and the goodput reading is the carrier delta, ~60 KB/s against
// ~7 MB/s. That leg fails BOTH halves of the ruling, so it is cut to a probe.
func TestOutclassedLegKeptToProbeBudget(t *testing.T) {
	m, tps, fwd := probeOnlyMux(t,
		legReading{basisMs: 755.9, delivBps: 7_400_000},
		legReading{basisMs: 9291.3, delivBps: 60_000})

	const frame = 16 * 1024
	const frames = 1024 // 16 MB, more than any of the failing 10 MB trials
	carried := download(t, m, tps, fwd, frames, frame)

	budget := uint64(LegProbeBytes()) //nolint:gosec // the default is positive
	// One frame may overshoot: the budget is charged after the send, so the
	// frame that crosses the line is already on the wire.
	limit := budget + frame
	if carried[1] > limit {
		t.Errorf("outclassed leg carried %d bytes, more than the probe budget %d (+1 frame): a 12x-slower, 1%%-goodput leg is still taking a proportional share",
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

// TestSlowButProductiveLegKeepsFullShare is the regression guard for the first
// live run of the gate (bench/2026-09-18/9f4848dfa-smoke): on legs-2 the healthy
// 166 ms leg beside the 44 ms one was cut five times — "322 ms against 53 ms",
// "270 vs 43", "613 vs 86", "833 vs 122", "843 vs 125" — and the set fell to
// x0.854 on a 50 MB download from x1.0-1.08 on develop. A delay basis is
// window/rate, so OUR OWN queue inflates it: the same leg was delivering 27-65 %
// of the bytes while reading 6-7x (carrier rows 11-13: 18.5/37.2, 10.7/39.4,
// 33.7/18.0 MB). The goodput half of the ruling is what keeps it.
func TestSlowButProductiveLegKeepsFullShare(t *testing.T) {
	for _, c := range []struct {
		name       string
		fast, slow legReading
	}{
		{"legs-2 row 11, 6.1x delay, 33 % of the bytes",
			legReading{basisMs: 53, delivBps: 3_720_000}, legReading{basisMs: 322, delivBps: 1_850_000}},
		{"legs-2 row 12, 6.9x delay, 21 % of the bytes",
			legReading{basisMs: 122, delivBps: 3_940_000}, legReading{basisMs: 833, delivBps: 1_070_000}},
		{"legs-2 row 13, 6.7x delay, the slow leg carrying MORE",
			legReading{basisMs: 125, delivBps: 1_800_000}, legReading{basisMs: 843, delivBps: 3_370_000}},
		{"the 44/166 ms pinned pair by first hop",
			legReading{basisMs: 44, delivBps: 4_000_000}, legReading{basisMs: 166, delivBps: 2_000_000}},
		{"ratio 10 on delay, but both bases under the absolute floor",
			legReading{basisMs: 20, delivBps: 4_000_000}, legReading{basisMs: 200, delivBps: 10_000}},
	} {
		m, tps, fwd := probeOnlyMux(t, c.fast, c.slow)
		if m.legProbeExhausted(1) {
			t.Errorf("%s: leg 1 was cut before carrying a byte", c.name)
		}
		carried := download(t, m, tps, fwd, 400, 16*1024)
		if carried[0] == 0 || carried[1] == 0 {
			t.Errorf("%s: the download must still spread across both legs; got %v", c.name, carried)
		}
	}
}

// TestColdLegIsNeverRuled pins the delivKnown guard: a leg that has not
// acknowledged anything yet is unmeasured, not unproductive, and cutting it
// would keep it that way.
func TestColdLegIsNeverRuled(t *testing.T) {
	m, _, _ := probeOnlyMux(t,
		legReading{basisMs: 200, delivBps: 5_000_000},
		legReading{basisMs: 5000, delivBps: 1_000})
	if !m.legProbeExhausted(1) {
		// Sanity: with a reading it IS ruled (and its probe is spent below).
		m.recordSent(1, uint64(LegProbeBytes())) //nolint:gosec // the default is positive
		if !m.legProbeExhausted(1) {
			t.Fatal("a 25x-delay leg delivering 0.02 percent of the best should be ruled probe-only")
		}
	}
	states := []ecfLegState{
		{rttMs: 200, delivBps: 5_000_000, delivKnown: true, ready: true},
		{rttMs: 5000, delivKnown: false, ready: true},
	}
	m.legMu.Lock()
	m.ruleProbeOnlyLegsLocked(states)
	m.legMu.Unlock()
	if m.legProbeExhausted(1) {
		t.Error("a leg with no delivery sample of its own must not be ruled outclassed")
	}
}

// TestProbeRulingIsReportedAndLifts proves the ruling is announced both ways and
// that it lifts on its own — here on the GOODPUT half, the leg starting to
// deliver again while its delay basis is still high, which is the case the
// legs-2 regression was.
func TestProbeRulingIsReportedAndLifts(t *testing.T) {
	m, tps, _ := probeOnlyMux(t,
		legReading{basisMs: 200, delivBps: 5_000_000},
		legReading{basisMs: 5000, delivBps: 10_000})

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

	// Re-rule with the same readings: no transition, so nothing is reported.
	rule(t, m, tps, legReading{basisMs: 200, delivBps: 5_000_000}, legReading{basisMs: 5000, delivBps: 10_000})
	if len(kinds) != 0 {
		t.Fatalf("a re-ruling with unchanged readings reported %d event(s)", len(kinds))
	}

	// Still 25x on delay, but now carrying a third of the bytes.
	rule(t, m, tps, legReading{basisMs: 200, delivBps: 5_000_000}, legReading{basisMs: 5000, delivBps: 2_500_000})
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

	m, tps, fwd := probeOnlyMux(t,
		legReading{basisMs: 755.9, delivBps: 7_400_000},
		legReading{basisMs: 9291.3, delivBps: 60_000})
	if m.legProbeExhausted(1) {
		t.Fatal("no leg may be gated with the ratio set past any real skew")
	}
	carried := download(t, m, tps, fwd, 400, 16*1024)
	if carried[1] == 0 {
		t.Error("with the gate off the download spreads as it did before")
	}
}

// TestDelivEWMADecaysWhenSilent proves a leg that stops acknowledging decays
// toward zero delivery rather than holding the rate it last proved — otherwise a
// leg that stalls outright would keep looking productive and never be ruled.
func TestDelivEWMADecaysWhenSilent(t *testing.T) {
	v := foldDeliv(0, 4_000_000)
	if v != 4_000_000 {
		t.Fatalf("first sample should seed the EWMA; got %g", v)
	}
	for i := 0; i < 20; i++ {
		v = foldDeliv(v, 0)
	}
	if v > 40_000 {
		t.Errorf("a leg silent for 20 refreshes still reads %g B/s", v)
	}
}
