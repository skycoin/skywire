package presets

import (
	"context"
	"testing"

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
		"adaptive":         "adaptive — the combined performance default: scales the mux leg count to load (AIMD), evicts the slowest leg toward low-latency convergence, and periodically probes a fresh path and keeps it only if it is better — one arbitrated action per tick.",
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
	if spec.Mux != 4 {
		t.Errorf("Mux = %d, want 4", spec.Mux)
	}
	if spec.MinHops != 2 {
		t.Errorf("MinHops = %d, want 2", spec.MinHops)
	}
	if spec.RotationIntervalSeconds != 90 {
		t.Errorf("RotationIntervalSeconds = %d, want 90", spec.RotationIntervalSeconds)
	}
	if spec.Distribution != "weighted: 1, 1, 1, 1" {
		t.Errorf("Distribution = %q, want weighted: 1, 1, 1, 1", spec.Distribution)
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
	if spec.Mux != 4 {
		t.Errorf("Mux = %d, want 4", spec.Mux)
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
	// adaptive starts LEAN at mux=1 (single fastest path) and grows under load
	// via on_tick; starting wide dragged traffic across slow disjoint siblings.
	if spec.Mux != 1 {
		t.Errorf("Mux = %d, want 1 (lean start, grow on load)", spec.Mux)
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
