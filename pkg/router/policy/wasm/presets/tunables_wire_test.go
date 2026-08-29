package presets

// tunables_wire_test.go proves the #4325 fix end-to-end: the runtime-tunable
// adaptive mux widths (adaptCap / adaptRevActive / adaptStandbyMax) actually
// REACH the sandboxed wazero guest and change its decide/tick output. Before the
// fix the guest ran on its own baked-in constants and the mux-control RPC /
// per-policy cli_overrides silently no-op'd on a preset:* (wasm) visor. The host
// stamps the values into the decide/tick input wire; the guest applies them
// before dispatch. These tests drive the REAL compiled bundle.wasm via wazero.
//
// Every test restores the process-global tunables to their defaults on exit so
// it never leaks into the parity tests (which assume defaults).

import (
	"context"
	"testing"

	"github.com/skycoin/skywire/pkg/router/policy"
	"github.com/skycoin/skywire/pkg/router/policy/preset"
	policywasm "github.com/skycoin/skywire/pkg/router/policy/wasm"
)

// restoreTunables snapshots the three tunables and returns a cleanup that puts
// them back — call as `defer restoreTunables()()`.
func restoreTunables() func() {
	cap, rev, standby := preset.AdaptCap(), preset.AdaptRevActive(), preset.AdaptStandbyMax()
	return func() {
		// Order matters (standby first raises the ceiling the others clamp to).
		preset.SetAdaptStandbyMax(standby)
		preset.SetAdaptCap(cap)
		preset.SetAdaptRevActive(rev)
	}
}

func newAdaptiveLoader(t *testing.T) *policywasm.Loader {
	t.Helper()
	l, err := policywasm.NewLoaderBytes("adaptive", Bundle(), policywasm.WithPreset("adaptive"))
	if err != nil {
		t.Fatalf("NewLoaderBytes: %v", err)
	}
	return l
}

// TestTunables_WireReachesGuest_Decide asserts the guest's decide Mux (which is
// AdaptRevActive()+AdaptStandbyMax()) tracks the HOST atomics — set via the RPC
// setters — proving the values crossed the wire into the sandboxed guest.
func TestTunables_WireReachesGuest_Decide(t *testing.T) {
	defer restoreTunables()()

	l := newAdaptiveLoader(t)
	defer l.Close() //nolint:errcheck

	rctx := policy.RoutingContext{App: "skysocks-client"}

	// Default: 1 (rev) + 512 (standby) = 513.
	if got, err := l.Decide(context.Background(), rctx, nil); err != nil {
		t.Fatalf("Decide (default): %v", err)
	} else if got.Mux != preset.AdaptRevActive()+preset.AdaptStandbyMax() {
		t.Fatalf("default Mux=%d want %d", got.Mux, preset.AdaptRevActive()+preset.AdaptStandbyMax())
	}

	// Retune via the RPC setters (the mux-control path). The guest must see them.
	preset.SetAdaptStandbyMax(10)
	preset.SetAdaptRevActive(3)
	got, err := l.Decide(context.Background(), rctx, nil)
	if err != nil {
		t.Fatalf("Decide (retuned): %v", err)
	}
	if want := 3 + 10; got.Mux != want {
		t.Fatalf("retuned Mux=%d want %d — the wired-in widths did not reach the wasm guest", got.Mux, want)
	}
}

// TestTunables_ConfigOverridesReachGuest_Decide asserts the per-policy config
// surface (cli_overrides) reaches the guest too: a policy carrying
// adapt_rev_active / adapt_standby_max in cli_overrides sizes the mux by them.
func TestTunables_ConfigOverridesReachGuest_Decide(t *testing.T) {
	defer restoreTunables()()

	l := newAdaptiveLoader(t)
	defer l.Close() //nolint:errcheck

	rctx := policy.RoutingContext{
		App:          "skysocks-client",
		CLIOverrides: map[string]string{"adapt_rev_active": "4", "adapt_standby_max": "20"},
	}
	got, err := l.Decide(context.Background(), rctx, nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if want := 4 + 20; got.Mux != want {
		t.Fatalf("cli_overrides Mux=%d want %d — per-policy tunables did not reach the wasm guest", got.Mux, want)
	}
}

// TestTunables_CapReachesGuest_Tick asserts the active-width CEILING crosses the
// wire: under sustained single-leg saturation the guest tick GROWS the active
// set (AddLeg) at the default cap, but a cap pinned to 1 suppresses that grow —
// so the cap the host set governs the guest's tick decision.
func TestTunables_CapReachesGuest_Tick(t *testing.T) {
	defer restoreTunables()()

	// A single active leg whose RecvBytes climbs every tick: seeds, then reads as
	// sustained-saturated, so rule (4) grows the active width once the saturation
	// streak clears hysteresis — provided aliveCount < adaptCap.
	steps := make([][]policy.LegInfo, 6)
	for i := range steps {
		steps[i] = []policy.LegInfo{{
			Index: 0, TransportID: "a", Kind: "stcpr", LatencyMs: 40, Alive: true,
			RecvBytes: uint64(1_000_000 * (i + 1)),
		}}
	}

	grewAtCap := func(cap int) bool {
		preset.SetAdaptStandbyMax(512)
		preset.SetAdaptCap(cap)
		preset.SetAdaptRevActive(1)
		l := newAdaptiveLoader(t)
		defer l.Close() //nolint:errcheck
		grew := false
		for i, legs := range steps {
			act, err := l.OnTick(context.Background(), policy.RoutingContext{App: "skysocks-client"}, legs)
			if err != nil {
				t.Fatalf("OnTick step %d (cap=%d): %v", i, cap, err)
			}
			if act.AddLeg {
				grew = true
			}
		}
		return grew
	}

	if !grewAtCap(8) {
		t.Fatal("expected the guest tick to grow the active set (AddLeg) under sustained saturation at cap=8")
	}
	if grewAtCap(1) {
		t.Fatal("cap=1 must suppress the guest tick's active-width grow — the cap did not reach the wasm guest")
	}
}
