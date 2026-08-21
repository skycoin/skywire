package presets

import (
	"context"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/router/policy"
	policywasm "github.com/skycoin/skywire/pkg/router/policy/wasm"
)

func TestNames(t *testing.T) {
	names := Names()
	want := map[string]bool{"app-mux": false, "rotating-bw": false, "latency-adaptive": false, "elastic-mux": false, "probe-and-prune": false, "adaptive": false}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, found := range want {
		if !found {
			t.Errorf("Names() missing %q; got %v", n, names)
		}
	}
}

func TestHas(t *testing.T) {
	for _, n := range []string{"app-mux", "rotating-bw", "latency-adaptive", "elastic-mux", "probe-and-prune", "adaptive"} {
		if !Has(n) {
			t.Errorf("Has(%q) = false, want true", n)
		}
	}
	if Has("does-not-exist") {
		t.Error("Has(does-not-exist) = true, want false")
	}
}

func TestBundle(t *testing.T) {
	if len(Bundle()) == 0 {
		t.Error("Bundle(): empty bytes")
	}
}

func TestDescribe(t *testing.T) {
	cases := map[string]string{
		"app-mux":          "app-mux — per-app static mux: vpn-client mux=4/min_hops=2, skychat single low-latency route, others default.",
		"rotating-bw":      "rotating-bw — mux=4 multi-hop for proxy/vpn/skynet with a leg rotated every 90s (bandwidth spread + traffic-analysis resistance).",
		"latency-adaptive": "latency-adaptive — mux=4 multi-hop that evicts the slowest leg each 30s (when its EWMA-smoothed latency is a >=1.5x-median outlier) until the leg set converges to low-latency disjoint paths, then holds (hysteresis-damped; no churn once converged).",
		"elastic-mux":      "elastic-mux — AIMD scaling of the mux leg count to load: grows a leg (up to 6) when the group is saturated, releases one (down to 2) when idle.",
		"probe-and-prune":  "probe-and-prune — periodically adds one speculative leg over a fresh path, observes its EWMA latency a few ticks, and keeps it only if it beats the current worst leg (continuous explore/exploit).",
		"adaptive":         "adaptive — the combined performance default (app-agnostic, chat excepted): a lean single forward leg plus a wider reverse (download) mux sized active+standby, proactively holding warm-standby spares for dip-free promotion; on_tick scales the active width to load (AIMD), evicts the slowest leg toward low-latency convergence, and periodically probes a fresh path and keeps it only if it is better — one arbitrated action per tick.",
	}
	for name, want := range cases {
		got, ok := Describe(name)
		if !ok {
			t.Errorf("Describe(%s): not found", name)
			continue
		}
		if got != want {
			t.Errorf("Describe(%s):\n got: %q\nwant: %q", name, got, want)
		}
	}
	if _, ok := Describe("does-not-exist"); ok {
		t.Error("Describe(does-not-exist): expected ok=false")
	}
}

// TestAppMuxDecides loads the embedded bundle with the app-mux preset
// stamped and asserts it decides mux=4/min_hops=2 for vpn-client —
// proving the combined module dispatches the app-mux logic correctly.
func TestAppMuxDecides(t *testing.T) {
	l, err := policywasm.NewLoaderBytes("app-mux", Bundle(),
		policywasm.WithPreset("app-mux"))
	if err != nil {
		t.Fatalf("NewLoaderBytes: %v", err)
	}
	defer l.Close() //nolint:errcheck

	spec, err := l.Decide(context.Background(),
		policy.RoutingContext{App: "vpn-client"}, nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if spec.Mux != 4 {
		t.Errorf("Mux = %d, want 4", spec.Mux)
	}
	if spec.MinHops != 2 {
		t.Errorf("MinHops = %d, want 2", spec.MinHops)
	}
}

// TestRotatingBWDecides loads the embedded bundle with the rotating-bw
// preset stamped and asserts a skysocks-client dial gets the mux=4
// multi-hop rotation spec — proving name dispatch selects the
// rotating-bw logic (distinct from app-mux, which leaves skysocks-client
// at defaults).
func TestRotatingBWDecides(t *testing.T) {
	l, err := policywasm.NewLoaderBytes("rotating-bw", Bundle(),
		policywasm.WithPreset("rotating-bw"))
	if err != nil {
		t.Fatalf("NewLoaderBytes: %v", err)
	}
	defer l.Close() //nolint:errcheck

	spec, err := l.Decide(context.Background(),
		policy.RoutingContext{App: "skysocks-client"}, nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	// targetMux(4) active + 1 warm standby = 5 legs provisioned, so the
	// rotation can hot-swap the spare in without dropping to 3 active.
	if spec.Mux != 5 {
		t.Errorf("Mux = %d, want 5 (targetMux 4 active + 1 warm standby)", spec.Mux)
	}
	if spec.MinHops != 2 {
		t.Errorf("MinHops = %d, want 2", spec.MinHops)
	}
	if spec.RotationIntervalSeconds != 90 {
		t.Errorf("RotationIntervalSeconds = %d, want 90", spec.RotationIntervalSeconds)
	}
	// Equal spread (not latency-weighted "auto") is the privacy property: no
	// single relay carries a large fraction of the flow.
	if spec.Distribution != "round-robin" {
		t.Errorf("Distribution = %q, want round-robin", spec.Distribution)
	}
}

// TestRotatingBWAppliesToCustomSession proves rotating-bw applies to a
// custom-named app/session (e.g. `proxy start --name g8`, which dials under
// ctx.App="g8"), not only the built-in binary names. The old app-name switch
// returned an empty spec for any custom session — so the policy silently never
// applied (no mux, no rotation). Any non-latency-sensitive app must get the
// full rotation spec.
func TestRotatingBWAppliesToCustomSession(t *testing.T) {
	l, err := policywasm.NewLoaderBytes("rotating-bw", Bundle(),
		policywasm.WithPreset("rotating-bw"))
	if err != nil {
		t.Fatalf("NewLoaderBytes: %v", err)
	}
	defer l.Close() //nolint:errcheck

	spec, err := l.Decide(context.Background(),
		policy.RoutingContext{App: "g8"}, nil) // a custom proxy session name
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if spec.Mux != 5 {
		t.Errorf("Mux = %d for custom session, want 5 (policy must apply, not no-op)", spec.Mux)
	}
	if spec.MinHops != 2 {
		t.Errorf("MinHops = %d, want 2", spec.MinHops)
	}
	if spec.RotationIntervalSeconds != 90 {
		t.Errorf("RotationIntervalSeconds = %d, want 90 (rotation must be scheduled)", spec.RotationIntervalSeconds)
	}
}

// TestRotatingBWRotatesWarmSwap proves the rotation is a warm hot-swap
// (promote the standby + demote the oldest active) and NOT the old
// drop+add tear-and-rebuild — the fix that stops higher-mux rotation from
// killing the stream. Feeds 4 active legs + 1 warm standby and asserts the
// action promotes the spare, demotes the oldest active, and drops nothing.
func TestRotatingBWRotatesWarmSwap(t *testing.T) {
	l, err := policywasm.NewLoaderBytes("rotating-bw", Bundle(),
		policywasm.WithPreset("rotating-bw"))
	if err != nil {
		t.Fatalf("NewLoaderBytes: %v", err)
	}
	defer l.Close() //nolint:errcheck

	// Indices 0..3 active, index 4 a warm standby.
	legs := []policy.LegInfo{
		{Index: 0, Kind: "stcpr", Alive: true},
		{Index: 1, Kind: "stcpr", Alive: true},
		{Index: 2, Kind: "sudph", Alive: true},
		{Index: 3, Kind: "stcpr", Alive: true},
		{Index: 4, Kind: "sudph", Alive: true, Standby: true},
	}
	act, err := l.OnTick(context.Background(),
		policy.RoutingContext{App: "skysocks-client"}, legs)
	if err != nil {
		t.Fatalf("OnTick: %v", err)
	}
	if len(act.PromoteFromStandby) != 1 || act.PromoteFromStandby[0] != 4 {
		t.Errorf("PromoteFromStandby = %v, want [4] (the warm spare)", act.PromoteFromStandby)
	}
	if len(act.DemoteToStandby) != 1 || act.DemoteToStandby[0] != 0 {
		t.Errorf("DemoteToStandby = %v, want [0] (the oldest active)", act.DemoteToStandby)
	}
	if len(act.DropLegs) != 0 {
		t.Errorf("DropLegs = %v, want none (warm swap never tears down a live leg)", act.DropLegs)
	}
	if act.AddLeg {
		t.Errorf("AddLeg = true, want false (no re-dial on a steady-state swap)")
	}
}

// TestRotatingBWParksWebRTC proves the type-aware standby management: when the
// active set is over target and contains a fragile webrtc leg (alongside a
// reliable anchor), on_tick PARKS the webrtc leg on standby (never drops it) and
// leaves the reliable leg active — so a webrtc drop can't collapse the group.
func TestRotatingBWParksWebRTC(t *testing.T) {
	l, err := policywasm.NewLoaderBytes("rotating-bw", Bundle(),
		policywasm.WithPreset("rotating-bw"))
	if err != nil {
		t.Fatalf("NewLoaderBytes: %v", err)
	}
	defer l.Close() //nolint:errcheck

	// 1 reliable (stcpr) + 4 fragile (webrtc) all active — over targetMux(4).
	legs := []policy.LegInfo{
		{Index: 0, Kind: "stcpr", Alive: true},
		{Index: 1, Kind: "webrtc", Alive: true},
		{Index: 2, Kind: "webrtc", Alive: true},
		{Index: 3, Kind: "webrtc", Alive: true},
		{Index: 4, Kind: "webrtc", Alive: true},
	}
	act, err := l.OnTick(context.Background(),
		policy.RoutingContext{App: "skysocks-client"}, legs)
	if err != nil {
		t.Fatalf("OnTick: %v", err)
	}
	if len(act.DropLegs) != 0 {
		t.Errorf("DropLegs = %v, want none (never drop — park on standby instead)", act.DropLegs)
	}
	if len(act.DemoteToStandby) != 1 {
		t.Fatalf("DemoteToStandby = %v, want exactly one leg parked", act.DemoteToStandby)
	}
	// The parked leg must be a webrtc one (index 1-4), never the reliable anchor (0).
	if act.DemoteToStandby[0] == 0 {
		t.Errorf("parked the reliable stcpr anchor (idx 0); want a webrtc leg parked instead")
	}
}

// TestLatencyAdaptiveDecides loads the embedded bundle with the
// latency-adaptive preset stamped and asserts a skysocks-client dial gets
// the asymmetric spec: a single lean upstream leg (forward_mux=1) paired
// with a 4-way multi-hop downstream fan-out (reverse_mux=4,
// reverse_min_hops=2) that re-evaluates every 30s — proving name dispatch
// selects the latency-adaptive logic.
func TestLatencyAdaptiveDecides(t *testing.T) {
	l, err := policywasm.NewLoaderBytes("latency-adaptive", Bundle(),
		policywasm.WithPreset("latency-adaptive"))
	if err != nil {
		t.Fatalf("NewLoaderBytes: %v", err)
	}
	defer l.Close() //nolint:errcheck

	spec, err := l.Decide(context.Background(),
		policy.RoutingContext{App: "skysocks-client"}, nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	// targetMux(4) active + 1 warm standby reserve = 5 legs provisioned, so
	// evicting the slowest leg is a no-dip hot-swap (promote spare, demote
	// outlier) rather than a cold drop+add.
	if spec.Mux != 5 {
		t.Errorf("Mux = %d, want 5 (targetMux 4 active + 1 warm standby)", spec.Mux)
	}
	if spec.MinHops != 2 {
		t.Errorf("MinHops = %d, want 2", spec.MinHops)
	}
	if spec.RotationIntervalSeconds != 30 {
		t.Errorf("RotationIntervalSeconds = %d, want 30", spec.RotationIntervalSeconds)
	}
}

// TestElasticMuxDecides loads the embedded bundle with the elastic-mux
// preset stamped and asserts a skysocks-client dial gets the modest seed
// spec (mux=2/min_hops=2, re-evaluated every 20s) that on_tick's AIMD
// controller then grows or shrinks — proving name dispatch selects the
// elastic-mux logic.
func TestElasticMuxDecides(t *testing.T) {
	l, err := policywasm.NewLoaderBytes("elastic-mux", Bundle(),
		policywasm.WithPreset("elastic-mux"))
	if err != nil {
		t.Fatalf("NewLoaderBytes: %v", err)
	}
	defer l.Close() //nolint:errcheck

	spec, err := l.Decide(context.Background(),
		policy.RoutingContext{App: "skysocks-client"}, nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if spec.Mux != 2 {
		t.Errorf("Mux = %d, want 2", spec.Mux)
	}
	if spec.MinHops != 2 {
		t.Errorf("MinHops = %d, want 2", spec.MinHops)
	}
	if spec.RotationIntervalSeconds != 20 {
		t.Errorf("RotationIntervalSeconds = %d, want 20", spec.RotationIntervalSeconds)
	}
}

// TestProbeAndPruneDecides loads the embedded bundle with the
// probe-and-prune preset stamped and asserts a skysocks-client dial gets
// the steady 3-way established spec (mux=3/min_hops=2, re-evaluated every
// 30s) that on_tick's explore/exploit state machine refines — proving name
// dispatch selects the probe-and-prune logic.
func TestProbeAndPruneDecides(t *testing.T) {
	l, err := policywasm.NewLoaderBytes("probe-and-prune", Bundle(),
		policywasm.WithPreset("probe-and-prune"))
	if err != nil {
		t.Fatalf("NewLoaderBytes: %v", err)
	}
	defer l.Close() //nolint:errcheck

	spec, err := l.Decide(context.Background(),
		policy.RoutingContext{App: "skysocks-client"}, nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if spec.Mux != 3 {
		t.Errorf("Mux = %d, want 3", spec.Mux)
	}
	if spec.MinHops != 2 {
		t.Errorf("MinHops = %d, want 2", spec.MinHops)
	}
	if spec.RotationIntervalSeconds != 30 {
		t.Errorf("RotationIntervalSeconds = %d, want 30", spec.RotationIntervalSeconds)
	}
}

// TestAdaptiveDecides loads the embedded bundle with the composite adaptive
// preset stamped and asserts a skysocks-client dial gets the converged middle
// spec (mux=3/min_hops=2, re-evaluated every 20s) that tickAdaptive then
// steers along all three performance dimensions — proving name dispatch
// selects the adaptive logic.
func TestAdaptiveDecides(t *testing.T) {
	l, err := policywasm.NewLoaderBytes("adaptive", Bundle(),
		policywasm.WithPreset("adaptive"))
	if err != nil {
		t.Fatalf("NewLoaderBytes: %v", err)
	}
	defer l.Close() //nolint:errcheck

	spec, err := l.Decide(context.Background(),
		policy.RoutingContext{App: "skysocks-client"}, nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	// adaptive is ASYMMETRIC by default: a single lean forward (request/upstream)
	// leg and a wider reverse (bulk download) mux sized active+standby, so the
	// router establishes warm spares up front and on_tick parks the surplus for
	// dip-free promotion. Symmetric Mux stays 0 (per-direction overrides win).
	if spec.Mux != 0 {
		t.Errorf("Mux = %d, want 0 (per-direction overrides drive adaptive)", spec.Mux)
	}
	if spec.ForwardMux != 1 {
		t.Errorf("ForwardMux = %d, want 1 (lean upstream)", spec.ForwardMux)
	}
	if spec.ReverseMux != 4 {
		t.Errorf("ReverseMux = %d, want 4 (2 active + 2 warm standby)", spec.ReverseMux)
	}
	// adaptive deliberately does NOT set MinHops: min-hops is the operator's
	// privacy constraint (session/config), not the performance policy's to
	// impose. 0 means "inherit the operator's floor" via EffectiveMinHops.
	if spec.MinHops != 0 {
		t.Errorf("MinHops = %d, want 0 (inherit operator floor)", spec.MinHops)
	}
	if spec.RotationIntervalSeconds != 20 {
		t.Errorf("RotationIntervalSeconds = %d, want 20", spec.RotationIntervalSeconds)
	}
}

// TestAdaptiveHotSwapsToWarmStandby drives the composite adaptive preset's
// on_tick with three active legs — one a >=1.5x-median latency outlier — plus
// one warm standby leg, and asserts it emits the NO-DIP HOT-SWAP: promote the
// parked spare AND demote the outlier in the SAME action, with no DropLegs and
// no AddLeg. This proves the gate-5 warm-standby primitive is not just wired
// through the ABI but actually exercised by the default policy: a degraded leg
// is swapped for a warm one with no setup-node round-trip and no width dip.
// See docs/warm_standby_legs_rfc.md.
func TestAdaptiveHotSwapsToWarmStandby(t *testing.T) {
	l, err := policywasm.NewLoaderBytes("adaptive", Bundle(),
		policywasm.WithPreset("adaptive"))
	if err != nil {
		t.Fatalf("NewLoaderBytes: %v", err)
	}
	defer l.Close() //nolint:errcheck

	// Three active legs: two fast (50ms), one slow (500ms) — a >=1.5x-median
	// outlier. Leg 3 is a warm standby (Alive but Standby), so the arbiter can
	// hot-swap instead of a cold drop+add. Distinct transport_ids so the guest's
	// per-transport EWMA seeds one smoothed sample each on this first tick.
	legs := []policy.LegInfo{
		{Index: 0, TransportID: "tid-a", Kind: "dmsg", LatencyMs: 50, Alive: true},
		{Index: 1, TransportID: "tid-b", Kind: "dmsg", LatencyMs: 50, Alive: true},
		{Index: 2, TransportID: "tid-c", Kind: "dmsg", LatencyMs: 500, Alive: true},
		{Index: 3, TransportID: "tid-d", Kind: "dmsg", LatencyMs: 40, Alive: true, Standby: true},
	}
	act, err := l.OnTick(context.Background(),
		policy.RoutingContext{App: "skysocks-client"}, legs)
	if err != nil {
		t.Fatalf("OnTick: %v", err)
	}
	if len(act.PromoteFromStandby) != 1 || act.PromoteFromStandby[0] != 3 {
		t.Errorf("PromoteFromStandby = %v, want [3] (the warm spare)", act.PromoteFromStandby)
	}
	if len(act.DemoteToStandby) != 1 || act.DemoteToStandby[0] != 2 {
		t.Errorf("DemoteToStandby = %v, want [2] (the latency outlier)", act.DemoteToStandby)
	}
	// The whole point of the hot-swap: no teardown, no cold add.
	if len(act.DropLegs) != 0 {
		t.Errorf("DropLegs = %v, want none (hot-swap must not tear down)", act.DropLegs)
	}
	if act.AddLeg {
		t.Error("AddLeg = true, want false (promote a warm spare, not a cold add)")
	}
}

// TestLatencyAdaptiveTrimsReserveToStandby drives latency-adaptive with all 5
// provisioned legs active (no outlier) and asserts it PARKS the oldest as a warm
// standby to reach the 4-wide active target — evict-by-demote, never a teardown.
// That parked leg is the reserve the hot-swap eviction later draws on. Exercises
// the shared classifyLegs/shedActive no-dip helpers the modernized performance
// presets (latency-adaptive / elastic-mux / probe-and-prune) all use.
func TestLatencyAdaptiveTrimsReserveToStandby(t *testing.T) {
	l, err := policywasm.NewLoaderBytes("latency-adaptive", Bundle(),
		policywasm.WithPreset("latency-adaptive"))
	if err != nil {
		t.Fatalf("NewLoaderBytes: %v", err)
	}
	defer l.Close() //nolint:errcheck

	// 5 active legs, comparable latency (no outlier). activeCount(5) > target(4)
	// → park the oldest (index 0) as a warm standby, no teardown.
	legs := []policy.LegInfo{
		{Index: 0, TransportID: "tid-a", Kind: "stcpr", LatencyMs: 100, Alive: true},
		{Index: 1, TransportID: "tid-b", Kind: "stcpr", LatencyMs: 100, Alive: true},
		{Index: 2, TransportID: "tid-c", Kind: "stcpr", LatencyMs: 100, Alive: true},
		{Index: 3, TransportID: "tid-d", Kind: "stcpr", LatencyMs: 100, Alive: true},
		{Index: 4, TransportID: "tid-e", Kind: "stcpr", LatencyMs: 100, Alive: true},
	}
	act, err := l.OnTick(context.Background(),
		policy.RoutingContext{App: "skysocks-client"}, legs)
	if err != nil {
		t.Fatalf("OnTick: %v", err)
	}
	if len(act.DemoteToStandby) != 1 || act.DemoteToStandby[0] != 0 {
		t.Errorf("DemoteToStandby = %v, want [0] (park oldest to reach target)", act.DemoteToStandby)
	}
	if len(act.DropLegs) != 0 {
		t.Errorf("DropLegs = %v, want none (never tear down)", act.DropLegs)
	}
	if act.AddLeg {
		t.Error("AddLeg = true, want false")
	}
}

// TestLatencyAdaptiveEvictsByHotSwap drives latency-adaptive at the 4-wide
// active target with one >=1.5x-median latency outlier plus a warm standby, and
// asserts a NO-DIP hot-swap: promote the spare AND demote the outlier in one
// action, no DropLegs, no AddLeg. This is the modernization's core claim —
// the slowest leg is swapped out for a warm one with no width dip and no
// re-dial, replacing the old cold drop+add.
func TestLatencyAdaptiveEvictsByHotSwap(t *testing.T) {
	l, err := policywasm.NewLoaderBytes("latency-adaptive", Bundle(),
		policywasm.WithPreset("latency-adaptive"))
	if err != nil {
		t.Fatalf("NewLoaderBytes: %v", err)
	}
	defer l.Close() //nolint:errcheck

	// 4 active legs (three 50ms, one 500ms outlier) + 1 warm standby (index 4).
	// activeCount==target(4); worst 500ms >= 1.5x median(50) → hot-swap it out.
	legs := []policy.LegInfo{
		{Index: 0, TransportID: "tid-a", Kind: "stcpr", LatencyMs: 50, Alive: true},
		{Index: 1, TransportID: "tid-b", Kind: "stcpr", LatencyMs: 50, Alive: true},
		{Index: 2, TransportID: "tid-c", Kind: "stcpr", LatencyMs: 50, Alive: true},
		{Index: 3, TransportID: "tid-d", Kind: "stcpr", LatencyMs: 500, Alive: true},
		{Index: 4, TransportID: "tid-e", Kind: "stcpr", LatencyMs: 40, Alive: true, Standby: true},
	}
	act, err := l.OnTick(context.Background(),
		policy.RoutingContext{App: "skysocks-client"}, legs)
	if err != nil {
		t.Fatalf("OnTick: %v", err)
	}
	if len(act.PromoteFromStandby) != 1 || act.PromoteFromStandby[0] != 4 {
		t.Errorf("PromoteFromStandby = %v, want [4] (warm spare)", act.PromoteFromStandby)
	}
	if len(act.DemoteToStandby) != 1 || act.DemoteToStandby[0] != 3 {
		t.Errorf("DemoteToStandby = %v, want [3] (the latency outlier)", act.DemoteToStandby)
	}
	if len(act.DropLegs) != 0 {
		t.Errorf("DropLegs = %v, want none (hot-swap, no teardown)", act.DropLegs)
	}
	if act.AddLeg {
		t.Error("AddLeg = true, want false")
	}
}

// --- conditional presets (constraint over route metadata) ------------------
//
// These load the recompiled bundle and prove — through the wazero runtime, not
// just the native Go logic — that each conditional preset dispatches by name
// and its decision HONORS ITS CONSTRAINT over synthetic candidates. Constraint-
// honoring is a deterministic property of the decision, so these need no live
// mesh (gate-4 for the conditional presets, minus the throughput numbers).

// TestGeoAvoidHonorsConstraint: with avoid_geo=US, the chosen route must not
// transit a US hop even though the US candidate is the fastest.
func TestGeoAvoidHonorsConstraint(t *testing.T) {
	l, err := policywasm.NewLoaderBytes("geo-avoid", Bundle(), policywasm.WithPreset("geo-avoid"))
	if err != nil {
		t.Fatalf("NewLoaderBytes: %v", err)
	}
	defer l.Close() //nolint:errcheck

	cands := []policy.Candidate{
		{Hops: []string{"a"}, HopsGeo: []string{"US"}, EstLatencyMs: 10},            // blocked, fastest
		{Hops: []string{"b"}, HopsGeo: []string{"DE"}, EstLatencyMs: 50},            // clean
		{Hops: []string{"c", "d"}, HopsGeo: []string{"FR", "US"}, EstLatencyMs: 20}, // blocked (2nd hop)
	}
	spec, err := l.Decide(context.Background(),
		policy.RoutingContext{App: "skysocks-client", CLIOverrides: map[string]string{"avoid_geo": "US"}}, cands)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if spec.Chosen == nil {
		t.Fatal("geo-avoid returned no Chosen candidate")
	}
	for _, g := range spec.Chosen.HopsGeo {
		if g == "US" {
			t.Fatalf("geo-avoid chose a route transiting a blocked country: %v", spec.Chosen.HopsGeo)
		}
	}
}

// TestTransportDiverseHonorsConstraint: the chosen route spans the most distinct
// transport types.
func TestTransportDiverseHonorsConstraint(t *testing.T) {
	l, err := policywasm.NewLoaderBytes("transport-diverse", Bundle(), policywasm.WithPreset("transport-diverse"))
	if err != nil {
		t.Fatalf("NewLoaderBytes: %v", err)
	}
	defer l.Close() //nolint:errcheck

	cands := []policy.Candidate{
		{Hops: []string{"a", "b"}, TransportKinds: []string{"stcpr", "stcpr"}, EstLatencyMs: 10},
		{Hops: []string{"c", "d"}, TransportKinds: []string{"stcpr", "sudph"}, EstLatencyMs: 30}, // most diverse
		{Hops: []string{"e", "f"}, TransportKinds: []string{"sudph", "sudph"}, EstLatencyMs: 5},
	}
	spec, err := l.Decide(context.Background(), policy.RoutingContext{App: "skysocks-client"}, cands)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if spec.Chosen == nil {
		t.Fatal("transport-diverse returned no Chosen candidate")
	}
	seen := map[string]bool{}
	for _, k := range spec.Chosen.TransportKinds {
		seen[k] = true
	}
	if len(seen) != 2 {
		t.Fatalf("transport-diverse must pick the most-diverse route; got kinds %v", spec.Chosen.TransportKinds)
	}
}

// TestTrustTieredHonorsConstraint: prefers the fully-trusted route even when a
// partially-trusted one is faster.
func TestTrustTieredHonorsConstraint(t *testing.T) {
	l, err := policywasm.NewLoaderBytes("trust-tiered", Bundle(), policywasm.WithPreset("trust-tiered"))
	if err != nil {
		t.Fatalf("NewLoaderBytes: %v", err)
	}
	defer l.Close() //nolint:errcheck

	cands := []policy.Candidate{
		{Hops: []string{"trustA", "evil"}, EstLatencyMs: 5},    // partial (faster)
		{Hops: []string{"trustA", "trustB"}, EstLatencyMs: 40}, // fully trusted
	}
	spec, err := l.Decide(context.Background(),
		policy.RoutingContext{App: "skysocks-client", CLIOverrides: map[string]string{"trusted_pks": "trustA,trustB"}}, cands)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if spec.Chosen == nil || len(spec.Chosen.Hops) != 2 || spec.Chosen.Hops[1] != "trustB" {
		t.Fatalf("trust-tiered must prefer the fully-trusted route; got %+v", spec.Chosen)
	}
}

// TestTimeOfDayHonorsConstraint: the shape is a deterministic function of the
// wall-clock hour stamped into the context.
func TestTimeOfDayHonorsConstraint(t *testing.T) {
	l, err := policywasm.NewLoaderBytes("time-of-day", Bundle(), policywasm.WithPreset("time-of-day"))
	if err != nil {
		t.Fatalf("NewLoaderBytes: %v", err)
	}
	defer l.Close() //nolint:errcheck

	biz, err := l.Decide(context.Background(),
		policy.RoutingContext{App: "skysocks-client", Now: time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)}, nil)
	if err != nil {
		t.Fatalf("Decide biz: %v", err)
	}
	if biz.Mux != 1 {
		t.Fatalf("business-hours (11:00) shape must be lean mux=1; got %d", biz.Mux)
	}
	off, err := l.Decide(context.Background(),
		policy.RoutingContext{App: "skysocks-client", Now: time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)}, nil)
	if err != nil {
		t.Fatalf("Decide off: %v", err)
	}
	if off.Mux != 4 || off.RotationIntervalSeconds == 0 {
		t.Fatalf("off-hours (03:00) shape must be a wide rotating mux; got %+v", off)
	}
}
