package presets

import (
	"context"
	"testing"

	"github.com/skycoin/skywire/pkg/router/policy"
	policywasm "github.com/skycoin/skywire/pkg/router/policy/wasm"
)

func TestNames(t *testing.T) {
	names := Names()
	want := map[string]bool{"app-mux": false, "rotating-bw": false}
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

func TestModule(t *testing.T) {
	mod, ok := Module("app-mux")
	if !ok {
		t.Fatal("Module(app-mux): not found")
	}
	if len(mod) == 0 {
		t.Error("Module(app-mux): empty bytes")
	}
	if _, ok := Module("does-not-exist"); ok {
		t.Error("Module(does-not-exist): expected ok=false")
	}
}

func TestDescribe(t *testing.T) {
	cases := map[string]string{
		"app-mux":     "app-mux — per-app static mux: vpn-client mux=4/min_hops=2, skychat single low-latency route, others default.",
		"rotating-bw": "rotating-bw — mux=4 multi-hop for proxy/vpn/skynet with a leg rotated every 90s (bandwidth spread + traffic-analysis resistance).",
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

// TestAppMuxDecides loads the embedded app-mux module via the WASM
// loader and asserts it decides mux=4/min_hops=2 for vpn-client — the
// same contract pkg/router/policy/wasm/loader_test.go exercises against
// the on-disk fixture, here proving the embedded copy is a working
// module.
func TestAppMuxDecides(t *testing.T) {
	mod, ok := Module("app-mux")
	if !ok {
		t.Fatal("Module(app-mux): not found")
	}
	l, err := policywasm.NewLoaderBytes("app-mux", mod)
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
