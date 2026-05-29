package visor

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/skycoin/skywire/pkg/router/policy"
)

// findRepoArtifact walks up from the test's runtime directory
// looking for path. Returns "" when not found.
func findRepoArtifact(t *testing.T, path ...string) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	dir := here
	for i := 0; i < 8; i++ {
		dir = filepath.Dir(dir)
		parts := append([]string{dir}, path...)
		candidate := filepath.Join(parts...)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func TestLoadPolicyEngine_DispatchesByExtension(t *testing.T) {
	wasmPath := findRepoArtifact(t, "docs", "examples", "routing-policies",
		"wasm", "app-mux", "app-mux.wasm")
	if wasmPath == "" {
		t.Skip("app-mux.wasm not built; see docs/examples/routing-policies/wasm/")
	}

	// .wasm path → WASM backend (tag carries the backend name).
	pe, err := loadPolicyEngine("@"+wasmPath, nil, nil)
	if err != nil {
		t.Fatalf("loadPolicyEngine(wasm): %v", err)
	}
	defer pe.engine.Close() //nolint:errcheck
	if pe.tag != "router.policy.wasm" {
		t.Errorf("wasm path picked tag %q, want router.policy.wasm", pe.tag)
	}

	// Empty / "none" → nil engine, no error.
	for _, raw := range []string{"", "none"} {
		got, err := loadPolicyEngine(raw, nil, nil)
		if err != nil {
			t.Errorf("loadPolicyEngine(%q): err=%v, want nil", raw, err)
		}
		if got != nil {
			t.Errorf("loadPolicyEngine(%q): engine=%v, want nil", raw, got)
		}
	}
}

func TestLoadPolicyEngine_DecidesViaWASM(t *testing.T) {
	wasmPath := findRepoArtifact(t, "docs", "examples", "routing-policies",
		"wasm", "app-mux", "app-mux.wasm")
	if wasmPath == "" {
		t.Skip("app-mux.wasm not built")
	}
	pe, err := loadPolicyEngine("@"+wasmPath, nil, nil)
	if err != nil {
		t.Fatalf("loadPolicyEngine: %v", err)
	}
	defer pe.engine.Close() //nolint:errcheck

	// Same expectations as the wasm package's own
	// TestWasmEvaluator_DecidesByApp — but here we go through
	// the visor-side helper, which is what production code uses.
	cases := []struct {
		app         string
		wantMux     int
		wantMinHops int
	}{
		{"vpn-client", 4, 2},
		{"skychat", 1, 0},
		{"skysocks-client", 0, 0},
	}
	for _, c := range cases {
		spec, err := pe.engine.Decide(context.Background(),
			policy.RoutingContext{App: c.app}, nil)
		if err != nil {
			t.Errorf("Decide(%s): %v", c.app, err)
			continue
		}
		if spec.Mux != c.wantMux {
			t.Errorf("Decide(%s): Mux=%d, want %d", c.app, spec.Mux, c.wantMux)
		}
		if spec.MinHops != c.wantMinHops {
			t.Errorf("Decide(%s): MinHops=%d, want %d", c.app, spec.MinHops, c.wantMinHops)
		}
	}
}

func TestHookRegisterApp_ReturnsPrevForRuntimeSwap(t *testing.T) {
	wasmPath := findRepoArtifact(t, "docs", "examples", "routing-policies",
		"wasm", "app-mux", "app-mux.wasm")
	if wasmPath == "" {
		t.Skip("app-mux.wasm not built")
	}

	pe1, err := loadPolicyEngine("@"+wasmPath, nil, nil)
	if err != nil {
		t.Fatalf("load #1: %v", err)
	}
	pe2, err := loadPolicyEngine("@"+wasmPath, nil, nil)
	if err != nil {
		t.Fatalf("load #2: %v", err)
	}
	defer pe2.engine.Close() //nolint:errcheck

	hook := policy.NewHook(nil)

	// First registration returns nil (no prior engine).
	if prev := hook.RegisterApp("vpn-client", pe1.engine); prev != nil {
		t.Errorf("first RegisterApp returned non-nil prev: %v", prev)
	}
	// Second registration returns the first engine — caller is
	// expected to Close it. SetAppRoutingPolicy's runtime swap
	// path relies on this contract.
	prev := hook.RegisterApp("vpn-client", pe2.engine)
	if prev == nil {
		t.Fatalf("second RegisterApp returned nil prev, want pe1.engine")
	}
	if prev != pe1.engine {
		t.Errorf("RegisterApp returned wrong prev engine")
	}
	if err := prev.Close(); err != nil {
		t.Errorf("Close prev: %v", err)
	}

	// Nil engine clears the registration; returns the current.
	prev2 := hook.RegisterApp("vpn-client", nil)
	if prev2 != pe2.engine {
		t.Errorf("RegisterApp(nil) returned wrong prev engine")
	}
}
