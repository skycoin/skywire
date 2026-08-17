package presets

import (
	"context"
	"testing"

	"github.com/skycoin/skywire/pkg/router/policy"
	policywasm "github.com/skycoin/skywire/pkg/router/policy/wasm"
)

func TestNames(t *testing.T) {
	names := Names()
	want := map[string]bool{"app-mux": false, "rotating-bw": false, "latency-adaptive": false}
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
	for _, n := range []string{"app-mux", "rotating-bw", "latency-adaptive"} {
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
		"latency-adaptive": "latency-adaptive — mux=4 multi-hop that evicts the slowest leg each 30s (when it is a >=1.5x-median outlier) until the leg set converges to low-latency disjoint paths, then holds (hysteresis-damped; no churn once converged).",
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
