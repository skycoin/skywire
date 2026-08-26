package preset

import (
	"reflect"
	"testing"
)

func TestDecide_ShapePresets(t *testing.T) {
	cases := []struct {
		name string
		ctx  Context
		want Spec
	}{
		{"app-mux/vpn", Context{App: "vpn-client"}, Spec{Mux: 4, MinHops: 2}},
		{"app-mux/skychat", Context{App: "skychat"}, Spec{Mux: 1}},
		{"app-mux/other", Context{App: "x"}, Spec{}},
		{"rotating-bw", Context{App: "skysocks-client"}, Spec{Mux: 5, MinHops: 2, RotationIntervalSeconds: 90, Distribution: "round-robin"}},
		{"rotating-bw/chat", Context{App: "skychat"}, Spec{Mux: 1}},
		{"latency-adaptive", Context{App: "vpn-client"}, Spec{Mux: 5, MinHops: 2, RotationIntervalSeconds: 30, Distribution: "auto"}},
		{"elastic-mux", Context{App: "skynet-client"}, Spec{Mux: 2, MinHops: 2, RotationIntervalSeconds: 20, Distribution: "auto"}},
		{"probe-and-prune", Context{App: "skynet-client"}, Spec{Mux: 3, MinHops: 2, RotationIntervalSeconds: 30, Distribution: "auto"}},
		{"adaptive", Context{App: "vpn-client"}, Spec{Mux: AdaptRevActive() + adaptStandbyMax, RotationIntervalSeconds: 20, Distribution: "capacity"}},
		{"adaptive/chat", Context{App: "skychat"}, Spec{Mux: 1}},
		{"adaptive/custom-session", Context{App: "g8"}, Spec{Mux: AdaptRevActive() + adaptStandbyMax, RotationIntervalSeconds: 20, Distribution: "capacity"}},
		{"ledbat", Context{App: "skysocks-client"}, Spec{Mux: 3, MinHops: 2, RotationIntervalSeconds: 20, Distribution: "auto"}},
		{"ledbat/chat", Context{App: "skychat"}, Spec{Mux: 1}},
	}
	for _, tc := range cases {
		presetName, _, _ := splitName(tc.name)
		got := Decide(presetName, tc.ctx, nil)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: Decide=%+v want %+v", tc.name, got, tc.want)
		}
	}
}

// splitName maps a "preset/variant" test label to the preset name.
func splitName(label string) (string, string, bool) {
	for i := 0; i < len(label); i++ {
		if label[i] == '/' {
			return label[:i], label[i+1:], true
		}
	}
	return label, "", false
}

func TestDecide_GeoAvoid(t *testing.T) {
	cands := []Candidate{
		{Hops: []string{"a"}, HopsGeo: []string{"US"}, EstLatencyMs: 10},
		{Hops: []string{"b"}, HopsGeo: []string{"DE"}, EstLatencyMs: 50},
	}
	got := Decide("geo-avoid", Context{CLIOverrides: map[string]string{"avoid_geo": "US"}}, cands)
	if got.Chosen == nil || got.Chosen.HopsGeo[0] != "DE" {
		t.Fatalf("geo-avoid should pick the clean DE route; got %+v", got.Chosen)
	}
	// No clean candidate → defer.
	got = Decide("geo-avoid", Context{CLIOverrides: map[string]string{"avoid_geo": "US,DE"}}, cands)
	if got.Chosen != nil {
		t.Fatalf("geo-avoid should defer when all violate; got %+v", got.Chosen)
	}
}

// TestDecide_Adaptive covers the transport-diversity refinement folded into the
// adaptive composite default: it seeds the most transport-diverse forward route
// when real kind metadata is present, but falls back to the router's pick (nil
// Chosen) with no candidates or only empty/unknown kinds — the wasm/NopProvider
// no-regression path. The seed shape (mux/rotation/distribution) and MinHops=0
// are unchanged, and non-client apps stay on the empty spec.
func TestDecide_Adaptive(t *testing.T) {
	diverse := []Candidate{
		{Hops: []string{"a"}, EstLatencyMs: 10, TransportKinds: []string{"stcpr"}},
		{Hops: []string{"b"}, EstLatencyMs: 50, TransportKinds: []string{"stcpr", "sudph"}},
		{Hops: []string{"c"}, EstLatencyMs: 30, TransportKinds: []string{"sudph"}},
	}

	// Real metadata present → pick the max-distinct-kinds candidate (b), and keep
	// the symmetric bidirectional seed shape otherwise (mux=active+standby,
	// rotation=20, auto, MinHops=0 inherited).
	got := Decide("adaptive", Context{App: "skysocks-client"}, diverse)
	if got.Chosen == nil || got.Chosen.Hops[0] != "b" {
		t.Fatalf("adaptive should seed the most transport-diverse route (b); got %+v", got.Chosen)
	}
	if got.Mux != AdaptRevActive()+adaptStandbyMax || got.ForwardMux != 0 || got.ReverseMux != 0 ||
		got.RotationIntervalSeconds != 20 || got.Distribution != "capacity" ||
		got.MinHops != 0 {
		t.Errorf("adaptive seed shape changed: %+v", got)
	}

	// Tiebreak on latency among equal diversity: a (lat 10) beats c (lat 30).
	oneKind := []Candidate{
		{Hops: []string{"c"}, EstLatencyMs: 30, TransportKinds: []string{"sudph"}},
		{Hops: []string{"a"}, EstLatencyMs: 10, TransportKinds: []string{"stcpr"}},
	}
	if got := Decide("adaptive", Context{App: "vpn-client"}, oneKind); got.Chosen == nil || got.Chosen.Hops[0] != "a" {
		t.Fatalf("adaptive tie should break to lowest latency (a); got %+v", got.Chosen)
	}

	// No candidates → fall back to the router's pick (nil Chosen), seed unchanged.
	if got := Decide("adaptive", Context{App: "skynet-client"}, nil); got.Chosen != nil {
		t.Fatalf("adaptive with no candidates must not force a route; got %+v", got.Chosen)
	}

	// Candidates present but every kind empty/unknown (wasm/NopProvider) → no
	// refinement, Chosen stays nil (no regression).
	unknown := []Candidate{
		{Hops: []string{"a"}, EstLatencyMs: 10, TransportKinds: []string{""}},
		{Hops: []string{"b"}, EstLatencyMs: 50},
	}
	if got := Decide("adaptive", Context{App: "skysocks-client"}, unknown); got.Chosen != nil {
		t.Fatalf("adaptive must not refine on empty/unknown kinds; got %+v", got.Chosen)
	}

	// Latency-sensitive chat → single lean route regardless of candidates
	// (no mux, no Chosen refinement).
	if got := Decide("adaptive", Context{App: "skychat"}, diverse); !reflect.DeepEqual(got, Spec{Mux: 1}) {
		t.Errorf("adaptive for chat should be a single lean route; got %+v", got)
	}

	// App-agnostic: an unknown / custom-named session still gets the adaptive
	// bidirectional mux (not the empty spec) — the fix must apply to EVERY app that
	// dials a route group, not a hardcoded allowlist.
	if got := Decide("adaptive", Context{App: "some-custom-app"}, nil); got.Mux != AdaptRevActive()+adaptStandbyMax || got.ForwardMux != 0 || got.ReverseMux != 0 {
		t.Errorf("adaptive must apply to any non-chat app; got %+v", got)
	}
}

// TestEngine_OnTick_AdaptiveHoldsWarmStandby asserts the adaptive default
// PROACTIVELY parks the surplus reverse legs as warm standby (down to the
// steady active target) instead of waiting for an idle signal, and never parks
// leg 0 (the primary / forward leg the router refuses to demote). The decide
// seeds AdaptRevActive()+adaptStandbyMax reverse legs; the router brings them up
// active; the first ticks must demote the newest surplus legs to standby.
func TestEngine_OnTick_AdaptiveHoldsWarmStandby(t *testing.T) {
	e := New()
	// AdaptRevActive()+adaptStandbyMax legs all active (as the router first
	// establishes them — every leg is born active). Steady active target =
	// AdaptRevActive().
	total := AdaptRevActive() + adaptStandbyMax
	legs := make([]LegInfo, total)
	for i := range legs {
		legs[i] = LegInfo{Index: i, TransportID: string(rune('a' + i)), Kind: "stcpr", LatencyMs: 40, Alive: true}
	}

	// ONE tick parks the WHOLE surplus (total-AdaptRevActive() legs) at once — the
	// bulk fast-converge that stops a wide uncapped mux from lingering as a
	// head-of-line-stalling active set. Leg 0 (primary) is never parked.
	act := e.OnTick("adaptive", legs)
	wantParked := total - AdaptRevActive()
	if len(act.DemoteToStandby) != wantParked {
		t.Fatalf("expected a bulk park of %d surplus legs in one tick, got %d: %+v",
			wantParked, len(act.DemoteToStandby), act)
	}
	parked := map[int]bool{}
	for _, idx := range act.DemoteToStandby {
		if idx == 0 {
			t.Fatalf("must never park leg 0 (primary/forward leg); got %+v", act.DemoteToStandby)
		}
		if parked[idx] {
			t.Fatalf("leg %d parked twice in one action", idx)
		}
		parked[idx] = true
		legs[idx].Standby = true // reflect the park, as the route group would
	}
	if len(parked) != wantParked {
		t.Fatalf("expected %d distinct parked legs, got %d", wantParked, len(parked))
	}

	// Steady state: AdaptRevActive() active + adaptStandbyMax standby → no further
	// structural change (no dip, no churn).
	if act := e.OnTick("adaptive", legs); !reflect.DeepEqual(act, RotationAction{}) {
		t.Errorf("at steady active target the adaptive tick must be a no-op; got %+v", act)
	}
	if parked[0] {
		t.Fatalf("leg 0 must stay active")
	}
}

// TestEngine_OnTick_AdaptiveParksSlowestFirst verifies the bulk proactive park
// sheds the HIGHEST-latency active legs first, so the set kept active is always
// the fastest desiredActive legs (never a slow leg while a faster one is parked).
func TestEngine_OnTick_AdaptiveParksSlowestFirst(t *testing.T) {
	e := New()
	// Hold the active target at 3 so the park leaves a >1 active set whose
	// membership we can check. Legs 0..5 active; distinct latencies. Leg 0 is
	// always kept; of the rest the two fastest must be kept, the three slowest
	// parked.
	e.adaptTarget = 3
	lat := []int{50, 500, 20, 300, 10, 400} // idx -> latency
	legs := make([]LegInfo, len(lat))
	for i := range legs {
		legs[i] = LegInfo{Index: i, TransportID: string(rune('a' + i)), Kind: "stcpr", LatencyMs: lat[i], Alive: true}
	}
	act := e.OnTick("adaptive", legs)
	// desiredActive=3 → park the 3 slowest of the 5 non-leg-0 legs: idx 5 (400),
	// idx 1 (500), idx 3 (300). Keep leg 0 (always) + idx 2 (20) + idx 4 (10).
	got := map[int]bool{}
	for _, i := range act.DemoteToStandby {
		got[i] = true
	}
	for _, slow := range []int{1, 3, 5} {
		if !got[slow] {
			t.Errorf("slowest legs must be parked first: leg %d (%dms) not parked; got %+v", slow, lat[slow], act.DemoteToStandby)
		}
	}
	for _, fast := range []int{0, 2, 4} {
		if got[fast] {
			t.Errorf("fastest legs must stay active: leg %d (%dms) was parked; got %+v", fast, lat[fast], act.DemoteToStandby)
		}
	}
}

func TestLegLowThroughput(t *testing.T) {
	// No median (fewer than 2 active rate samples) → never an outlier.
	if legLowThroughput(0, 0) || legLowThroughput(10, 0) {
		t.Error("unknown median must never classify a leg as a low-throughput outlier")
	}
	// Below adaptThroughputOutlierFrac (0.25) of the median → outlier.
	if !legLowThroughput(10, 100) {
		t.Error("10 vs median 100 (0.10) must be a low-throughput outlier")
	}
	if !legLowThroughput(24, 100) {
		t.Error("24 vs median 100 (0.24 < 0.25) must be a low-throughput outlier")
	}
	// At/above the fraction → healthy (carrying a fair share).
	if legLowThroughput(30, 100) {
		t.Error("30 vs median 100 (0.30 > 0.25) must be healthy")
	}
}

// TestEngine_OnTick_AdaptiveEvictsLowThroughputLeg drives a loaded group where
// one active leg (never leg 0) delivers a fraction of what its peers do — a
// webrtc-style low-bandwidth path — and asserts the tick evicts it from the
// active mux (park or hot-swap) after the health hysteresis, so it stops
// dragging the no-skip reorder buffer. Latencies are equal so the ONLY signal
// is throughput.
func TestEngine_OnTick_AdaptiveEvictsLowThroughputLeg(t *testing.T) {
	e := New()
	e.adaptTarget = 4 // keep 4 active so nothing is parked for being surplus
	// A warm standby (idx 4) so the eviction can hot-swap rather than starve.
	recv := []uint64{0, 0, 0, 0, 0}
	mk := func() []LegInfo {
		legs := make([]LegInfo, 5)
		for i := range legs {
			legs[i] = LegInfo{Index: i, TransportID: string(rune('a' + i)), Kind: "stcpr", LatencyMs: 50, Alive: true, RecvBytes: recv[i]}
		}
		legs[4].Standby = true
		return legs
	}
	evicted := false
	for tick := 0; tick < 8; tick++ {
		// Legs 1,2,3 carry a full share; leg 3 carries ~2% (low outlier).
		recv[1] += 100000
		recv[2] += 100000
		recv[3] += 2000
		act := e.OnTick("adaptive", mk())
		for _, idx := range act.DemoteToStandby {
			if idx == 3 {
				evicted = true
			}
			if idx == 0 {
				t.Fatalf("tick %d: must never evict leg 0; got %+v", tick, act)
			}
		}
		if evicted {
			break
		}
	}
	if !evicted {
		t.Error("a sustained low-throughput active leg must be evicted from the active mux")
	}
}

// TestEngine_OnTick_AdaptiveSwapsBadPrimary asserts that a SUSTAINED gross-
// outlier primary (leg 0) — the one leg every other rule exempts — is swapped
// out via DropLegs:[0] once a healthy active alternative exists, so a bad
// primary (e.g. a webrtc-second-hop leg dragging at >ceiling latency) can't ride
// forever. The drop lets the router promote a healthy aux into the primary slot.
func TestEngine_OnTick_AdaptiveSwapsBadPrimary(t *testing.T) {
	e := New()
	e.adaptTarget = 4 // 4 active, no surplus to park (isolates the primary swap)
	mk := func() []LegInfo {
		return []LegInfo{
			{Index: 0, TransportID: "p", Kind: "stcpr", LatencyMs: 2000, Alive: true}, // gross-outlier primary (> ceiling)
			{Index: 1, TransportID: "a", Kind: "stcpr", LatencyMs: 100, Alive: true},
			{Index: 2, TransportID: "b", Kind: "stcpr", LatencyMs: 110, Alive: true},
			{Index: 3, TransportID: "c", Kind: "stcpr", LatencyMs: 90, Alive: true},
		}
	}
	swapped := false
	for tick := 0; tick < 6; tick++ {
		act := e.OnTick("adaptive", mk())
		for _, idx := range act.DropLegs {
			if idx == 0 {
				swapped = true
			}
		}
		if swapped {
			break
		}
	}
	if !swapped {
		t.Error("a sustained gross-outlier primary with a healthy alternative must be swapped out via DropLegs:[0]")
	}

	// A HEALTHY primary must never be swapped (no churn on a good primary).
	e2 := New()
	e2.adaptTarget = 4
	good := []LegInfo{
		{Index: 0, TransportID: "p", Kind: "stcpr", LatencyMs: 95, Alive: true},
		{Index: 1, TransportID: "a", Kind: "stcpr", LatencyMs: 100, Alive: true},
		{Index: 2, TransportID: "b", Kind: "stcpr", LatencyMs: 110, Alive: true},
		{Index: 3, TransportID: "c", Kind: "stcpr", LatencyMs: 90, Alive: true},
	}
	for tick := 0; tick < 6; tick++ {
		act := e2.OnTick("adaptive", good)
		for _, idx := range act.DropLegs {
			if idx == 0 {
				t.Fatalf("tick %d: a healthy primary must never be swapped; got %+v", tick, act)
			}
		}
	}
}

// TestEngine_OnTick_AdaptiveCapsActiveUnderLoad asserts the active set is capped
// at AdaptCap() even when the group reads SATURATED. During the uncap fill, legs
// are born active and route-setup traffic keeps the group saturated — the park
// must still cap active at AdaptCap() (parking the slowest excess) so the reorder
// buffer never spans more than AdaptCap() legs; only born-active excess is shed,
// never below AdaptCap() under load.
func TestEngine_OnTick_AdaptiveCapsActiveUnderLoad(t *testing.T) {
	// Pin the runtime-tunable cap to 8 so this test exercises the capping logic
	// deterministically regardless of the shipped default (which is now 60).
	restoreCap := AdaptCap()
	SetAdaptCap(8)
	defer SetAdaptCap(restoreCap)

	e := seedSaturatedAdaptive(1)
	const n = 20
	legs := make([]LegInfo, n)
	for i := range legs {
		tid := string(rune('a' + i))
		var recv uint64
		if i == 0 {
			tid = "t0"       // matches seedSaturatedAdaptive's prevRecv key
			recv = 1_000_000 // fresh delta keeps the group saturated this tick
		}
		// distinct latencies: leg i = 20+i*10 ms, so higher index = slower.
		legs[i] = LegInfo{Index: i, TransportID: tid, Kind: "stcpr", LatencyMs: 20 + i*10, Alive: true, RecvBytes: recv}
	}
	act := e.OnTick("adaptive", legs)
	wantParked := n - AdaptCap()
	if len(act.DemoteToStandby) != wantParked {
		t.Fatalf("under load, active must be capped at AdaptCap()=%d: want %d parked, got %d: %+v",
			AdaptCap(), wantParked, len(act.DemoteToStandby), act)
	}
	parked := map[int]bool{}
	for _, idx := range act.DemoteToStandby {
		if idx == 0 {
			t.Fatalf("must never park leg 0; got %+v", act.DemoteToStandby)
		}
		parked[idx] = true
	}
	// The AdaptCap() kept-active legs are the fastest (leg 0 + legs 1..AdaptCap()-1);
	// the n-AdaptCap() slowest (highest index) are parked.
	for i := AdaptCap(); i < n; i++ {
		if !parked[i] {
			t.Errorf("slowest excess legs must be parked to cap active at AdaptCap(): leg %d (%dms) not parked", i, 20+i*10)
		}
	}
	for i := 1; i < AdaptCap(); i++ {
		if parked[i] {
			t.Errorf("fastest legs must stay active under load: leg %d (%dms) was parked", i, 20+i*10)
		}
	}
}

func TestDecide_TimeOfDay(t *testing.T) {
	const h = int64(3600) * 1_000_000_000
	if got := Decide("time-of-day", Context{NowUnixNano: 11 * h}, nil); got.Mux != 1 {
		t.Errorf("business hours should be lean (mux 1); got %+v", got)
	}
	off := Decide("time-of-day", Context{NowUnixNano: 3 * h}, nil)
	if off.Mux != 4 || off.Distribution != "round-robin" || off.RotationIntervalSeconds == 0 {
		t.Errorf("off-hours should be a wide rotating mux; got %+v", off)
	}
}

// seedSaturatedAdaptive puts an Engine into a deterministically SATURATED view
// with its steady active target at `target`, so a single OnTick exercises the
// saturation-growth path without a multi-tick warmup. The two active legs t0/t1
// carry half of `peak` in fresh RecvBytes each (prevRecv seeded to 0), so the
// tick recomputes throughputEWMA==peak → saturated. Same-package test reaches
// the unexported controller state directly.
// seedSaturatedAdaptive puts an Engine into a deterministically SATURATED view
// with its steady active target at `target` and the sustained-saturation streak
// already met, so a single OnTick exercises the saturation-growth path without a
// multi-tick warmup. The active leg t0 carries `peak` fresh RecvBytes
// (prevRecv==0) so the tick recomputes throughputEWMA==peak → saturated.
// Same-package test reaches the unexported controller state directly.
func seedSaturatedAdaptive(target int) *Engine {
	const peak = 1_000_000.0
	e := New()
	e.adaptSeeded = true
	e.adaptThroughputEWMA = peak
	e.adaptPeak = peak
	e.adaptTarget = target
	e.adaptSatTicks = adaptHysteresis
	e.adaptPrevRecv["t0"] = 0
	return e
}

// activeLeg / standbyLeg build the two leg shapes the tick tests present.
func activeLeg(idx int, tid string, recv uint64) LegInfo {
	return LegInfo{Index: idx, TransportID: tid, Kind: "stcpr", LatencyMs: 40, Alive: true, RecvBytes: recv}
}

func standbyLeg(idx int, tid string) LegInfo {
	return LegInfo{Index: idx, TransportID: tid, Kind: "stcpr", LatencyMs: 40, Alive: true, Standby: true}
}

// TestEngine_OnTick_AdaptiveStandbyFloor asserts the always-on warm-standby
// floor: under sustained saturation the reserve is never drained below
// adaptStandbyMin (load grows the ACTIVE width with a fresh leg instead), yet
// surplus standby ABOVE the floor is still usable for load, and an active-leg
// DROP is covered by an instant promote plus a re-establish that restores the
// floor.
func TestEngine_OnTick_AdaptiveStandbyFloor(t *testing.T) {
	// (A) At the floor + sustained saturation → grow with a FRESH leg, never
	// drain the last warm spare. One active leg (== target 1) + one standby at
	// the floor.
	e := seedSaturatedAdaptive(1)
	atFloor := []LegInfo{
		activeLeg(0, "t0", 1_000_000),
		standbyLeg(1, "s0"),
	}
	if got := e.OnTick("adaptive", atFloor); !reflect.DeepEqual(got, RotationAction{AddLeg: true}) {
		t.Errorf("saturated at the standby floor must AddLeg (not drain the reserve); got %+v", got)
	}

	// (B) Surplus standby ABOVE the floor + saturated → the surplus spare may be
	// promoted for load (efficient), keeping the floor intact.
	e = seedSaturatedAdaptive(1)
	surplus := []LegInfo{
		activeLeg(0, "t0", 1_000_000),
		standbyLeg(1, "s0"),
		standbyLeg(2, "s1"),
	}
	got := e.OnTick("adaptive", surplus)
	if len(got.PromoteFromStandby) != 1 || got.AddLeg {
		t.Errorf("saturated with surplus standby should promote the surplus spare (no AddLeg); got %+v", got)
	}

	// (C) An active leg DROPPED (active < target) → promote a warm spare INSTANTLY
	// and re-establish a replacement, restoring the floor with zero re-dial dip.
	e = seedSaturatedAdaptive(2) // target 2 but only one active leg present
	dropped := []LegInfo{
		activeLeg(0, "t0", 1_000_000),
		standbyLeg(1, "s0"),
		standbyLeg(2, "s1"),
	}
	got = e.OnTick("adaptive", dropped)
	if len(got.PromoteFromStandby) != 1 || !got.AddLeg {
		t.Errorf("an active-leg drop must promote a spare AND re-establish one (restore the floor); got %+v", got)
	}

	// (D) Sustained saturation, modeled end-to-end: the standby reserve must never
	// fall below adaptStandbyMin across many heavy-load ticks, and the active
	// width must grow.
	eng := New()
	active, standby := AdaptRevActive(), adaptStandbyMax
	var recv uint64
	build := func() []LegInfo {
		legs := make([]LegInfo, 0, active+standby)
		for i := 0; i < active; i++ {
			legs = append(legs, activeLeg(i, "a"+string(rune('0'+i)), recv))
		}
		for i := 0; i < standby; i++ {
			legs = append(legs, standbyLeg(active+i, "s"+string(rune('0'+i))))
		}
		return legs
	}
	for tick := 0; tick < 40; tick++ {
		recv += 5_000_000 // heavy sustained load on every active leg
		act := eng.OnTick("adaptive", build())
		for range act.PromoteFromStandby {
			if standby > 0 {
				standby--
				active++
			}
		}
		for range act.DemoteToStandby {
			if active > 1 {
				active--
				standby++
			}
		}
		if act.AddLeg {
			active++
		}
		for range act.DropLegs {
			if active > 0 {
				active--
			}
		}
		if standby < adaptStandbyMin {
			t.Fatalf("tick %d: standby reserve %d fell below floor %d (action %+v)", tick, standby, adaptStandbyMin, act)
		}
	}
	if active <= AdaptRevActive() {
		t.Errorf("sustained saturation should grow the active width beyond the %d-leg seed; active=%d", AdaptRevActive(), active)
	}
	if standby < adaptStandbyMin {
		t.Errorf("standby reserve ended below floor: %d < %d", standby, adaptStandbyMin)
	}
}

// TestEngine_OnTick_AdaptiveEvictsGrossOutlier asserts the health gate: a
// gross-latency-outlier active leg is kept OUT of the active mux (evicted after
// the hysteresis window), while a leg merely slower-than-median is NOT evicted
// (only GROSS outliers, so the set doesn't churn on normal variance). Leg 0 is
// never evicted.
func TestEngine_OnTick_AdaptiveEvictsGrossOutlier(t *testing.T) {
	// Two active legs at the target (leg 0 healthy, leg 1 a 2000ms gross outlier)
	// plus a healthy warm spare. Target 2 so the set is at steady width (no
	// grow/park), isolating the health decision.
	e := New()
	e.adaptTarget = 2
	legs := []LegInfo{
		{Index: 0, TransportID: "t0", Kind: "stcpr", LatencyMs: 50, Alive: true},
		{Index: 1, TransportID: "t1", Kind: "stcpr", LatencyMs: 2000, Alive: true},
		{Index: 2, TransportID: "s0", Kind: "stcpr", LatencyMs: 45, Alive: true, Standby: true},
	}
	// Below the hysteresis threshold: no reshape yet (a single spike must not
	// churn the active set).
	for i := 0; i < adaptHysteresis-1; i++ {
		if got := e.OnTick("adaptive", legs); !reflect.DeepEqual(got, RotationAction{}) {
			t.Fatalf("tick %d: outlier below hysteresis must not reshape; got %+v", i, got)
		}
	}
	// Once sustained, the outlier (leg 1, never leg 0) is evicted — hot-swapped
	// for the healthy warm spare (promote s0 @2, demote leg 1).
	got := e.OnTick("adaptive", legs)
	if len(got.DemoteToStandby) != 1 || got.DemoteToStandby[0] != 1 {
		t.Errorf("sustained gross outlier (leg 1) must be evicted from the active set; got %+v", got)
	}
	if len(got.PromoteFromStandby) != 1 || got.PromoteFromStandby[0] != 2 {
		t.Errorf("eviction should hot-swap the healthy warm spare in; got %+v", got)
	}
	if got.DropLegs != nil {
		t.Errorf("hot-swap must not tear down; got %+v", got)
	}
}

// TestEngine_OnTick_AdaptiveStableUnderSteady asserts anti-churn: under steady
// (sustained-bulk) conditions the active set does NOT flap every tick — the
// hysteresis + cooldown rate-limit reshapes, and the set CONVERGES to a stable
// width and then stops reshaping (a long tail of no-ops). This is the fix for
// the observed "active set churns constantly, disrupting in-flight flows."
func TestEngine_OnTick_AdaptiveStableUnderSteady(t *testing.T) {
	// Pin the runtime-tunable widths to the values this test's convergence/churn
	// assertions were written against (cap 8, floor 1), independent of the shipped
	// defaults (now 60 / 4). Set the cap first so the low floor is not clamped, and
	// restore in reverse.
	restoreCap := AdaptCap()
	SetAdaptCap(8)
	defer SetAdaptCap(restoreCap)
	restoreW := AdaptRevActive()
	SetAdaptRevActive(1)
	defer SetAdaptRevActive(restoreW)

	eng := New()
	active, standby := AdaptRevActive(), adaptStandbyMax
	var recv uint64
	build := func() []LegInfo {
		legs := make([]LegInfo, 0, active+standby)
		for i := 0; i < active; i++ {
			legs = append(legs, activeLeg(i, "a"+string(rune('0'+i)), recv))
		}
		for i := 0; i < standby; i++ {
			legs = append(legs, standbyLeg(active+i, "s"+string(rune('0'+i))))
		}
		return legs
	}
	reshapes, tail := 0, 0
	for tick := 0; tick < 40; tick++ {
		recv += 5_000_000
		act := eng.OnTick("adaptive", build())
		if len(act.PromoteFromStandby)+len(act.DemoteToStandby)+len(act.DropLegs) > 0 || act.AddLeg {
			reshapes++
			tail = 0
		} else {
			tail++
		}
		for range act.PromoteFromStandby {
			if standby > 0 {
				standby--
				active++
			}
		}
		for range act.DemoteToStandby {
			if active > 1 {
				active--
				standby++
			}
		}
		if act.AddLeg {
			active++
		}
		for range act.DropLegs {
			if active > 0 {
				active--
			}
		}
	}
	// Anti-churn: far fewer reshapes than ticks (cooldown + hysteresis rate-limit
	// — not a change every tick).
	if reshapes > 12 {
		t.Errorf("too many reshapes under steady load (churn): %d in 40 ticks", reshapes)
	}
	// Convergence: the set settles onto a stable width and holds (a long tail of
	// no-ops), rather than perpetually reshaping.
	if tail < 5 {
		t.Errorf("adaptive did not converge to a stable set; trailing no-ops=%d", tail)
	}
}

func TestEngine_OnTick_UnknownIsNoop(t *testing.T) {
	e := New()
	if got := e.OnTick("app-mux", []LegInfo{{Index: 0, Alive: true}}); !reflect.DeepEqual(got, RotationAction{}) {
		t.Errorf("app-mux has no tick logic; want no-op, got %+v", got)
	}
}

func TestEngine_OnTick_RotatingBWParksFragile(t *testing.T) {
	e := New()
	// A reliable active leg + a fragile (webrtc) active leg → park the fragile.
	legs := []LegInfo{
		{Index: 0, TransportID: "t0", Kind: "stcpr", Alive: true},
		{Index: 1, TransportID: "t1", Kind: "webrtc", Alive: true},
	}
	got := e.OnTick("rotating-bw", legs)
	want := RotationAction{DemoteToStandby: []int{1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rotating-bw should park the fragile active leg; got %+v want %+v", got, want)
	}
}

// TestEngine_OnTick_LedbatBacksOffOnQueuingDelay drives the ledbat scavenger
// with three active legs where leg 2's delay climbs far above its base (min)
// delay while legs 0 and 1 stay near theirs. Once leg 2's smoothed queuing delay
// exceeds the target, the controller must BACK OFF by parking the highest-delay
// non-primary active leg (leg 2) to warm standby — yielding capacity — and never
// tear a leg down or touch leg 0.
func TestEngine_OnTick_LedbatBacksOffOnQueuingDelay(t *testing.T) {
	e := New()
	// Legs 0,1 hold a steady ~40ms; leg 2 starts at its base 40ms then spikes to
	// 300ms so its EWMA rises well past base+target (60ms).
	steady := func(lat2 int) []LegInfo {
		return []LegInfo{
			{Index: 0, TransportID: "t0", Kind: "stcpr", LatencyMs: 40, Alive: true},
			{Index: 1, TransportID: "t1", Kind: "stcpr", LatencyMs: 40, Alive: true},
			{Index: 2, TransportID: "t2", Kind: "stcpr", LatencyMs: lat2, Alive: true},
		}
	}
	// First tick seeds base=40 for every leg (EWMA==sample), no queuing yet.
	if got := e.OnTick("ledbat", steady(40)); !reflect.DeepEqual(got, RotationAction{}) {
		t.Fatalf("seed tick: at base delay, no queuing → no-op; got %+v", got)
	}
	// Now leg 2 congests. Feed the spike until its EWMA queuing delay crosses the
	// target and the controller backs off (parks leg 2). Bounded loop.
	var got RotationAction
	for i := 0; i < 10; i++ {
		got = e.OnTick("ledbat", steady(300))
		if len(got.DemoteToStandby) > 0 {
			break
		}
		if !reflect.DeepEqual(got, RotationAction{}) {
			t.Fatalf("pre-backoff tick %d: expected no-op or the backoff demote; got %+v", i, got)
		}
	}
	if len(got.DemoteToStandby) != 1 || got.DemoteToStandby[0] != 2 {
		t.Fatalf("ledbat must park the highest-queuing-delay non-primary leg (2); got %+v", got)
	}
	if got.DropLegs != nil {
		t.Errorf("back-off parks, never tears down; got DropLegs=%v", got.DropLegs)
	}
	if len(got.PromoteFromStandby) != 0 || got.AddLeg {
		t.Errorf("back-off must not grow; got %+v", got)
	}
}

// TestEngine_OnTick_LedbatNeverParksBelowFloor asserts the yield floor: even when
// the sole non-primary active leg is congesting, ledbat shrinks only to a single
// active leg (leg 0) and then holds — it never parks leg 0, so the flow always
// keeps one leg making progress.
func TestEngine_OnTick_LedbatNeverParksBelowFloor(t *testing.T) {
	e := New()
	legs := func(lat1 int, oneStandby bool) []LegInfo {
		return []LegInfo{
			{Index: 0, TransportID: "t0", Kind: "stcpr", LatencyMs: 40, Alive: true},
			{Index: 1, TransportID: "t1", Kind: "stcpr", LatencyMs: lat1, Alive: true, Standby: oneStandby},
		}
	}
	// Seed base=40 for both legs.
	e.OnTick("ledbat", legs(40, false))
	// Congest leg 1 until it is parked, then confirm at the floor (leg 0 only
	// active, leg 1 standby) the controller does NOT keep demoting.
	parked := false
	for i := 0; i < 10 && !parked; i++ {
		got := e.OnTick("ledbat", legs(300, false))
		if len(got.DemoteToStandby) == 1 && got.DemoteToStandby[0] == 1 {
			parked = true
		} else if len(got.DemoteToStandby) > 0 {
			t.Fatalf("must only ever park leg 1 (never leg 0); got %+v", got)
		}
	}
	if !parked {
		t.Fatal("leg 1 never parked despite sustained queuing delay")
	}
	// At the floor (1 active), a still-congested snapshot must be a no-op — leg 0
	// is never parked.
	if got := e.OnTick("ledbat", legs(300, true)); len(got.DemoteToStandby) != 0 || got.DropLegs != nil {
		t.Errorf("at the yield floor ledbat must hold, never park leg 0; got %+v", got)
	}
}

// TestEngine_OnTick_LedbatGrowsWhenNoQueuing asserts the grow rule: with a parked
// reserve leg and every active leg reading near its base (no self-induced
// queuing), the controller re-promotes a parked leg — up to the ledbatMux cap —
// drawing only on the reserve (no fresh dial).
func TestEngine_OnTick_LedbatGrowsWhenNoQueuing(t *testing.T) {
	e := New()
	// Leg 0 active near base, legs 1 and 2 parked. No queuing anywhere.
	legs := []LegInfo{
		{Index: 0, TransportID: "t0", Kind: "stcpr", LatencyMs: 40, Alive: true},
		{Index: 1, TransportID: "t1", Kind: "stcpr", LatencyMs: 40, Alive: true, Standby: true},
		{Index: 2, TransportID: "t2", Kind: "stcpr", LatencyMs: 40, Alive: true, Standby: true},
	}
	got := e.OnTick("ledbat", legs)
	if len(got.PromoteFromStandby) != 1 || got.PromoteFromStandby[0] != 1 {
		t.Fatalf("no queuing + below cap → promote the lowest-index parked leg (1); got %+v", got)
	}
	if got.AddLeg {
		t.Errorf("grow must draw on the reserve, never dial fresh; got AddLeg=true")
	}
	if len(got.DemoteToStandby) != 0 || got.DropLegs != nil {
		t.Errorf("grow must not shed; got %+v", got)
	}
}

// TestDecide_Coupled pins the coupled preset's decide shape: a modest symmetric
// mux over the multi-hop overlay with best-leg (auto) byte weighting for target
// apps, a single lean route for chat, defaults for everything else.
func TestDecide_Coupled(t *testing.T) {
	if got := Decide("coupled", Context{App: "skysocks-client"}, nil); !reflect.DeepEqual(
		got, Spec{Mux: 4, MinHops: 2, RotationIntervalSeconds: 20, Distribution: "auto"}) {
		t.Errorf("coupled/proxy: Decide=%+v", got)
	}
	if got := Decide("coupled", Context{App: "skychat"}, nil); !reflect.DeepEqual(got, Spec{Mux: 1}) {
		t.Errorf("coupled/chat: Decide=%+v want {Mux:1}", got)
	}
	if got := Decide("coupled", Context{App: "other"}, nil); !reflect.DeepEqual(got, Spec{}) {
		t.Errorf("coupled/other: Decide=%+v want {}", got)
	}
}

// TestEngine_OnTick_CoupledShedsWorstOnLoss drives the coupled controller with a
// clean 4-wide active set, then makes one leg's retransmits rise, and asserts the
// COUPLED DECREASE: the worst (lossy) leg is shed to standby — a lone demote,
// concentrating traffic on the good legs.
func TestEngine_OnTick_CoupledShedsWorstOnLoss(t *testing.T) {
	e := New()
	base := func(retrans2 uint64) []LegInfo {
		return []LegInfo{
			{Index: 0, TransportID: "t0", Kind: "stcpr", LatencyMs: 50, Alive: true},
			{Index: 1, TransportID: "t1", Kind: "stcpr", LatencyMs: 50, Alive: true},
			{Index: 2, TransportID: "t2", Kind: "stcpr", LatencyMs: 50, Alive: true, Retransmits: retrans2},
			{Index: 3, TransportID: "t3", Kind: "stcpr", LatencyMs: 50, Alive: true},
		}
	}
	// Tick 1 establishes the retransmit baseline; a clean 4-wide set at the ceiling
	// is a no-op (no grow, no loss to shed).
	if got := e.OnTick("coupled", base(0)); !reflect.DeepEqual(got, RotationAction{}) {
		t.Fatalf("baseline tick must be a no-op; got %+v", got)
	}
	// Tick 2: leg 2's retransmits jumped → rising loss → shed the worst leg (2).
	got := e.OnTick("coupled", base(100))
	if len(got.DemoteToStandby) != 1 || got.DemoteToStandby[0] != 2 {
		t.Errorf("coupled must shed the lossy leg (2) to standby; got %+v", got)
	}
	if got.AddLeg || len(got.PromoteFromStandby) != 0 || len(got.DropLegs) != 0 {
		t.Errorf("coupled decrease must be a lone demote (no grow/drop); got %+v", got)
	}
}

// TestEngine_OnTick_CoupledCautiousPromoteWhenClean asserts the COUPLED INCREASE:
// a loss-free active set below the ceiling with a warm spare promotes AT MOST one
// leg (LIA's cautious coupled increase).
func TestEngine_OnTick_CoupledCautiousPromoteWhenClean(t *testing.T) {
	e := New()
	legs := []LegInfo{
		{Index: 0, TransportID: "t0", Kind: "stcpr", LatencyMs: 50, Alive: true},
		{Index: 1, TransportID: "t1", Kind: "stcpr", LatencyMs: 50, Alive: true},
		{Index: 2, TransportID: "t2", Kind: "stcpr", LatencyMs: 50, Alive: true},
		{Index: 3, TransportID: "s0", Kind: "stcpr", LatencyMs: 45, Alive: true, Standby: true},
	}
	got := e.OnTick("coupled", legs)
	if len(got.PromoteFromStandby) != 1 || got.PromoteFromStandby[0] != 3 {
		t.Errorf("clean active set below the ceiling must cautiously promote the spare; got %+v", got)
	}
	if got.AddLeg || len(got.DemoteToStandby) != 0 || len(got.DropLegs) != 0 {
		t.Errorf("cautious increase must be a lone promote; got %+v", got)
	}
}

// TestEngine_OnTick_CoupledNoGrowUnderLoss is the coupling property: even with a
// warm spare available and the active set below the ceiling, rising loss on an
// active leg FORBIDS growth — the controller sheds the lossy leg instead of
// promoting. (The loss baseline is seeded white-box so the rise reads on the very
// first tick, without an intervening clean tick consuming the spare.)
func TestEngine_OnTick_CoupledNoGrowUnderLoss(t *testing.T) {
	e := New()
	e.coupledPrevRetransmits["t1"] = 0
	legs := []LegInfo{
		{Index: 0, TransportID: "t0", Kind: "stcpr", LatencyMs: 50, Alive: true},
		{Index: 1, TransportID: "t1", Kind: "stcpr", LatencyMs: 50, Alive: true, Retransmits: 100},
		{Index: 2, TransportID: "t2", Kind: "stcpr", LatencyMs: 50, Alive: true},
		{Index: 3, TransportID: "s0", Kind: "stcpr", LatencyMs: 45, Alive: true, Standby: true},
	}
	got := e.OnTick("coupled", legs)
	if len(got.PromoteFromStandby) != 0 {
		t.Errorf("coupled must NOT grow while an active leg shows rising loss (a spare is available); got %+v", got)
	}
	if len(got.DemoteToStandby) != 1 || got.DemoteToStandby[0] != 1 {
		t.Errorf("coupled should shed the lossy leg (1) instead; got %+v", got)
	}
}

// --- adaptive BIDIRECTIONAL sizing (forward/upload widening) ---
//
// These exercise the SentBytes-driven forward controller added alongside the
// existing RecvBytes-driven reverse controller. adaptiveSim is a faithful
// mini-router: it feeds the engine a leg snapshot, applies the returned
// RotationAction to the leg set (append on AddLeg/AddForwardLeg, flip Standby on
// promote/demote, remove on drop), then advances the cumulative byte counters
// for the next tick — so the engine sees leg growth react to its own actions.
type adaptiveSim struct {
	e           *Engine
	legs        []LegInfo
	nextTID     int
	sawAddFwd   bool
	sawAddLeg   bool
	sawPromote  bool
	sentPerTick uint64 // bytes added to each ACTIVE leg's SentBytes each tick
	recvPerTick uint64 // bytes added to each ACTIVE leg's RecvBytes each tick
}

func newAdaptiveSim(sentPerTick, recvPerTick uint64) *adaptiveSim {
	s := &adaptiveSim{e: New(), sentPerTick: sentPerTick, recvPerTick: recvPerTick, nextTID: 1}
	// Steady start: one active forward/primary leg (leg 0). No standby — the
	// simplest shape that isolates the sizing controllers.
	s.legs = []LegInfo{{Index: 0, TransportID: "t0", Kind: "stcpr", LatencyMs: 40, Alive: true}}
	return s
}

func (s *adaptiveSim) activeCount() int {
	n := 0
	for _, l := range s.legs {
		if l.Alive && !l.Standby {
			n++
		}
	}
	return n
}

func (s *adaptiveSim) reindex() {
	for i := range s.legs {
		s.legs[i].Index = i
	}
}

func (s *adaptiveSim) step() RotationAction { //nolint:unparam // test helper: return kept for call-site clarity
	act := s.e.OnTick("adaptive", s.legs)
	if act.AddForwardLeg {
		s.sawAddFwd = true
	}
	if act.AddLeg {
		s.sawAddLeg = true
	}
	if len(act.PromoteFromStandby) > 0 {
		s.sawPromote = true
	}
	// Apply promote/demote first (index-stable), then drops (compact), then adds.
	for _, idx := range act.PromoteFromStandby {
		if idx >= 0 && idx < len(s.legs) {
			s.legs[idx].Standby = false
		}
	}
	for _, idx := range act.DemoteToStandby {
		if idx >= 0 && idx < len(s.legs) {
			s.legs[idx].Standby = true
		}
	}
	if len(act.DropLegs) > 0 {
		drop := map[int]bool{}
		for _, idx := range act.DropLegs {
			drop[idx] = true
		}
		var kept []LegInfo
		for i, l := range s.legs {
			if !drop[i] {
				kept = append(kept, l)
			}
		}
		s.legs = kept
		s.reindex()
	}
	if act.AddLeg || act.AddForwardLeg {
		tid := "t" + string(rune('a'+s.nextTID)) //nolint:gosec // G115: bounded test rune
		s.nextTID++
		s.legs = append(s.legs, LegInfo{Index: len(s.legs), TransportID: tid, Kind: "stcpr", LatencyMs: 40, Alive: true})
	}
	// Advance cumulative counters on the ACTIVE legs for the next snapshot.
	for i := range s.legs {
		if s.legs[i].Alive && !s.legs[i].Standby {
			s.legs[i].SentBytes += s.sentPerTick
			s.legs[i].RecvBytes += s.recvPerTick
		}
	}
	return act
}

// TestEngine_OnTick_AdaptiveForwardWidensOnUpload asserts an upload-heavy flow
// (growing SentBytes, flat RecvBytes) widens the FORWARD mux via AddForwardLeg
// while the reverse controller stays completely lean (no AddLeg, reverse target
// unchanged at AdaptRevActive()).
func TestEngine_OnTick_AdaptiveForwardWidensOnUpload(t *testing.T) {
	// Pin the widths to the values this test asserts against (cap 8, floor 1),
	// independent of the shipped defaults (now 60 / 4). Pin BEFORE newAdaptiveSim
	// so New() seeds adaptTarget from the pinned floor. Set cap first, restore in
	// reverse.
	restoreCap := AdaptCap()
	SetAdaptCap(8)
	defer SetAdaptCap(restoreCap)
	restoreW := AdaptRevActive()
	SetAdaptRevActive(1)
	defer SetAdaptRevActive(restoreW)

	s := newAdaptiveSim(1_000_000, 0) // heavy upload, zero download
	for i := 0; i < 20; i++ {
		s.step()
	}
	if !s.sawAddFwd {
		t.Fatalf("upload-heavy flow must widen forward via AddForwardLeg; never saw one")
	}
	if s.sawAddLeg {
		t.Errorf("upload-heavy flow must NOT grow the reverse/full-duplex set (AddLeg)")
	}
	if s.e.adaptTarget != AdaptRevActive() {
		t.Errorf("reverse target must stay lean at %d; got %d", AdaptRevActive(), s.e.adaptTarget)
	}
	if s.e.adaptFwdTarget <= adaptFwdActive {
		t.Errorf("forward target must have widened above %d; got %d", adaptFwdActive, s.e.adaptFwdTarget)
	}
	if s.activeCount() <= 1 {
		t.Errorf("forward widening must have added at least one active send leg; active=%d", s.activeCount())
	}
}

// TestEngine_OnTick_AdaptiveReverseWidensOnDownload is the REGRESSION guard: a
// download-heavy flow (growing RecvBytes, flat SentBytes) must still widen the
// reverse set via AddLeg exactly as before, and must NOT trip the new forward
// controller (no AddForwardLeg; forward target stays at adaptFwdActive).
func TestEngine_OnTick_AdaptiveReverseWidensOnDownload(t *testing.T) {
	s := newAdaptiveSim(0, 1_000_000) // zero upload, heavy download
	for i := 0; i < 20; i++ {
		s.step()
	}
	if !s.sawAddLeg && !s.sawPromote {
		t.Fatalf("download-heavy flow must widen the reverse set (AddLeg/promote); saw neither")
	}
	if s.sawAddFwd {
		t.Errorf("download-heavy flow must NOT trip the forward controller (AddForwardLeg)")
	}
	if s.e.adaptFwdTarget != adaptFwdActive {
		t.Errorf("forward target must stay lean at %d on a download flow; got %d", adaptFwdActive, s.e.adaptFwdTarget)
	}
	if s.e.adaptTarget <= AdaptRevActive() {
		t.Errorf("reverse target must have widened above %d; got %d", AdaptRevActive(), s.e.adaptTarget)
	}
}

// TestEngine_OnTick_AdaptiveForwardCollapsesOnIdle asserts a forward-widened
// flow collapses back to the lean single forward leg once the upload goes idle
// (forward target returns to adaptFwdActive and the active set shrinks).
func TestEngine_OnTick_AdaptiveForwardCollapsesOnIdle(t *testing.T) {
	// Pin the widths to the values this test asserts against (cap 8, floor 1) so
	// the idle steady state collapses to a SINGLE active leg, independent of the
	// shipped defaults (now 60 / 4). Pin BEFORE newAdaptiveSim. Set cap first,
	// restore in reverse.
	restoreCap := AdaptCap()
	SetAdaptCap(8)
	defer SetAdaptCap(restoreCap)
	restoreW := AdaptRevActive()
	SetAdaptRevActive(1)
	defer SetAdaptRevActive(restoreW)

	s := newAdaptiveSim(1_000_000, 0)
	for i := 0; i < 20; i++ { // widen under upload
		s.step()
	}
	if s.e.adaptFwdTarget <= adaptFwdActive {
		t.Fatalf("precondition: forward must be widened; got fwdTarget=%d", s.e.adaptFwdTarget)
	}
	grown := s.activeCount()
	// Upload stops: flat SentBytes and RecvBytes from here on.
	s.sentPerTick, s.recvPerTick = 0, 0
	for i := 0; i < 40; i++ {
		s.step()
	}
	if s.e.adaptFwdTarget != adaptFwdActive {
		t.Errorf("idle must collapse forward target back to %d; got %d", adaptFwdActive, s.e.adaptFwdTarget)
	}
	if s.activeCount() >= grown {
		t.Errorf("idle must shed the extra forward legs; active stayed %d (peak %d)", s.activeCount(), grown)
	}
	if s.activeCount() != 1 {
		t.Errorf("idle steady state must be a single active forward leg; active=%d", s.activeCount())
	}
}
