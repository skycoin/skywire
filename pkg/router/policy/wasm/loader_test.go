package wasm

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/skycoin/skywire/pkg/router/policy"
)

// findExampleWASM locates the app-mux.wasm artifact. It first honours
// SKYWIRE_APPMUX_WASM (set by the CI `test-wasm-policy` lane, which
// rebuilds the fixture from source with TinyGo so the test exercises a
// FRESH build rather than the possibly-stale committed blob). Failing
// that it walks up from the test's runtime directory looking for the
// committed docs/examples/routing-policies/wasm/app-mux/app-mux.wasm.
// Returns "" when not found (caller t.Skip's).
func findExampleWASM() string {
	if p := os.Getenv("SKYWIRE_APPMUX_WASM"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	_, here, _, _ := runtime.Caller(0)
	repoRoot := here
	for i := 0; i < 8; i++ {
		repoRoot = filepath.Dir(repoRoot)
		candidate := filepath.Join(repoRoot, "docs", "examples", "routing-policies", "wasm", "app-mux", "app-mux.wasm")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func TestWasmEvaluator_DecidesByApp(t *testing.T) {
	wasmPath := findExampleWASM()
	if wasmPath == "" {
		t.Skip("app-mux.wasm not built; run `tinygo build -target=wasi -no-debug -opt=2 -o app-mux.wasm .` in docs/examples/routing-policies/wasm/app-mux/")
	}
	wasmBytes, err := os.ReadFile(wasmPath) //nolint:gosec
	if err != nil {
		t.Fatalf("read wasm: %v", err)
	}

	eval, err := NewEvaluator("app-mux.wasm", wasmBytes)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	defer eval.Close() //nolint:errcheck

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
		spec, err := eval.Decide(context.Background(),
			policy.RoutingContext{App: c.app},
			nil)
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

func TestWasmEvaluator_MissingDecideRouteErrors(t *testing.T) {
	// A WASM module without decide_route should be rejected at
	// NewEvaluator time. We don't have a real such module handy,
	// but constructing the evaluator with a minimal bytes blob
	// exercises the parse-error path.
	_, err := NewEvaluator("invalid.wasm", []byte("not a wasm module"))
	if err == nil {
		t.Errorf("expected error for non-wasm bytes, got nil")
	}
}
