// Package policy pkg/router/policy/hook_test.go — pins the
// Loader → DialHook adapter. Tests the loader-side semantics
// (script defines decide_route returning mux/min_hops/fallback)
// translate cleanly into the router-facing DialAdjustment shape.
package policy

import (
	"context"
	"errors"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
)

func TestHook_AdjustsMuxAndMinHops(t *testing.T) {
	src := `
def decide_route(ctx, candidates):
    if ctx.app == "vpn-client":
        return RouteSpec(mux = 4, min_hops = 2)
    return RouteSpec()
`
	loader, err := NewLoader(src)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader)

	pk := cipher.PubKey{}
	pk[0] = 0x02 // any non-null PK

	cases := []struct {
		app          string
		wantMux      int
		wantMinHops  int
		wantFallback string
	}{
		{"vpn-client", 4, 2, ""},
		{"skychat", 0, 0, ""},
	}
	for _, tc := range cases {
		adj, err := h.BeforeDial(context.Background(), router.DialInfo{
			AppName: tc.app,
			PeerPK:  pk,
			LPort:   routing.Port(1),
			RPort:   routing.Port(2),
		})
		if err != nil {
			t.Errorf("BeforeDial(%s): %v", tc.app, err)
			continue
		}
		if adj.MuxRoutes != tc.wantMux {
			t.Errorf("app=%s: MuxRoutes=%d, want %d", tc.app, adj.MuxRoutes, tc.wantMux)
		}
		if adj.MinHops != tc.wantMinHops {
			t.Errorf("app=%s: MinHops=%d, want %d", tc.app, adj.MinHops, tc.wantMinHops)
		}
		if adj.Fallback != tc.wantFallback {
			t.Errorf("app=%s: Fallback=%q, want %q", tc.app, adj.Fallback, tc.wantFallback)
		}
	}
}

func TestHook_DropFallback(t *testing.T) {
	src := `
def decide_route(ctx, candidates):
    if ctx.app == "blocked-app":
        return RouteSpec(fallback = "drop")
    return RouteSpec()
`
	loader, err := NewLoader(src)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader)
	pk := cipher.PubKey{}
	pk[0] = 0x02

	adj, err := h.BeforeDial(context.Background(), router.DialInfo{
		AppName: "blocked-app",
		PeerPK:  pk,
	})
	if err != nil {
		t.Fatalf("BeforeDial: %v", err)
	}
	if adj.Fallback != "drop" {
		t.Errorf("Fallback=%q, want %q", adj.Fallback, "drop")
	}
}

func TestHook_NoopLoader_ReturnsEmptyAdjustment(t *testing.T) {
	loader, err := NewLoader("")
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader)
	adj, err := h.BeforeDial(context.Background(), router.DialInfo{AppName: "x"})
	if err != nil {
		t.Errorf("BeforeDial: %v", err)
	}
	if adj.MuxRoutes != 0 || adj.MinHops != 0 || adj.Fallback != "" {
		t.Errorf("noop loader: expected empty adjustment, got %+v", adj)
	}
}

func TestHook_SelectRoute_PicksByGeo(t *testing.T) {
	// Indonesia-Friday-style filter, distilled: pick whichever
	// candidate route includes a hop in country "ID". Three input
	// candidates with different geo footprints; only candidate 1
	// touches ID.
	src := `
def decide_route(ctx, candidates):
    for c in candidates:
        if "ID" in c.hops_geo:
            return RouteSpec(chosen = c)
    return RouteSpec()
`
	hopA := "020000000000000000000000000000000000000000000000000000000000000001"
	hopB := "020000000000000000000000000000000000000000000000000000000000000002"
	hopC := "020000000000000000000000000000000000000000000000000000000000000003"

	prov := NewFakeProvider().
		SetGeo(hopA, "DE").
		SetGeo(hopB, "ID").
		SetGeo(hopC, "US")

	loader, err := NewLoader(src, WithProvider(prov))
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader, WithHookProvider(prov))
	pk := cipher.PubKey{}
	pk[0] = 0x02

	cands := []router.CandidateInfo{
		{Hops: []string{hopA, hopC}}, // DE, US
		{Hops: []string{hopA, hopB}}, // DE, ID — should win
		{Hops: []string{hopC}},       // US
	}
	sel, err := h.SelectRoute(context.Background(), router.DialInfo{AppName: "skychat", PeerPK: pk}, cands, nil)
	if err != nil {
		t.Fatalf("SelectRoute: %v", err)
	}
	if sel.Drop {
		t.Fatalf("unexpected drop")
	}
	if sel.Chosen != 1 {
		t.Errorf("Chosen=%d, want 1 (the ID-touching candidate)", sel.Chosen)
	}
}

func TestHook_SelectRoute_NoMatchDefers(t *testing.T) {
	// Script returns an empty RouteSpec when nothing matches —
	// hook surfaces Chosen=-1 so the router falls back to its
	// built-in disjoint-path pick.
	src := `
def decide_route(ctx, candidates):
    for c in candidates:
        if "ZZ" in c.hops_geo:
            return RouteSpec(chosen = c)
    return RouteSpec()
`
	hopA := "020000000000000000000000000000000000000000000000000000000000000001"
	prov := NewFakeProvider().SetGeo(hopA, "DE")

	loader, err := NewLoader(src, WithProvider(prov))
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader, WithHookProvider(prov))
	pk := cipher.PubKey{}
	pk[0] = 0x02
	sel, err := h.SelectRoute(context.Background(), router.DialInfo{AppName: "x", PeerPK: pk},
		[]router.CandidateInfo{{Hops: []string{hopA}}}, nil)
	if err != nil {
		t.Fatalf("SelectRoute: %v", err)
	}
	if sel.Chosen != -1 {
		t.Errorf("Chosen=%d, want -1 (defer to router pick)", sel.Chosen)
	}
}

func TestHook_DropMode_ErrorPropagates(t *testing.T) {
	// Script that always errors. With FailureDrop, the loader
	// propagates the error; the hook surfaces it as a non-nil
	// error from BeforeDial. Router-side, that's logged and
	// the dial proceeds with unchanged opts (the router's own
	// failure-safety; see init_router.go).
	src := `
def decide_route(ctx, candidates):
    return 1 / 0
`
	loader, err := NewLoader(src, WithFailureMode(FailureDrop))
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader)
	pk := cipher.PubKey{}
	pk[0] = 0x02
	_, err = h.BeforeDial(context.Background(), router.DialInfo{AppName: "x", PeerPK: pk})
	if err == nil {
		t.Fatal("expected non-nil error in drop mode")
	}
	if !errors.Is(err, err) { // just exercises the path; errors.Is here is trivially true
		t.Skip()
	}
}

func TestHook_BeforeDial_DistributionDescriptorParsed(t *testing.T) {
	src := `
def decide_route(ctx, candidates):
    return RouteSpec(mux=2, distribution="weighted: 3, 1")
`
	loader, err := NewLoader(src)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader)
	pk := cipher.PubKey{}
	pk[0] = 0x02
	adj, err := h.BeforeDial(context.Background(), router.DialInfo{AppName: "x", PeerPK: pk})
	if err != nil {
		t.Fatalf("BeforeDial: %v", err)
	}
	if adj.MuxRoutes != 2 {
		t.Errorf("MuxRoutes=%d, want 2", adj.MuxRoutes)
	}
	if adj.Distribution.Mode != router.DistributionWeighted {
		t.Errorf("Distribution.Mode=%v, want DistributionWeighted", adj.Distribution.Mode)
	}
	if len(adj.Distribution.Weights) != 2 || adj.Distribution.Weights[0] != 3 || adj.Distribution.Weights[1] != 1 {
		t.Errorf("Distribution.Weights=%v, want [3 1]", adj.Distribution.Weights)
	}
}

func TestHook_BeforeDial_DistributionSizeThreshold(t *testing.T) {
	src := `
def decide_route(ctx, candidates):
    return RouteSpec(mux=2, distribution="size-threshold: 1400")
`
	loader, err := NewLoader(src)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader)
	pk := cipher.PubKey{}
	pk[0] = 0x02
	adj, err := h.BeforeDial(context.Background(), router.DialInfo{AppName: "x", PeerPK: pk})
	if err != nil {
		t.Fatalf("BeforeDial: %v", err)
	}
	if adj.Distribution.Mode != router.DistributionSizeThreshold {
		t.Errorf("Distribution.Mode=%v, want DistributionSizeThreshold", adj.Distribution.Mode)
	}
	if adj.Distribution.SizeThreshold != 1400 {
		t.Errorf("Distribution.SizeThreshold=%d, want 1400", adj.Distribution.SizeThreshold)
	}
}

func TestHook_BeforeDial_BadDistributionLogsAndContinues(t *testing.T) {
	src := `
def decide_route(ctx, candidates):
    return RouteSpec(mux=2, distribution="not-a-real-descriptor")
`
	loader, err := NewLoader(src)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	var logged string
	h := NewHook(loader, WithHookLogger(func(format string, _ ...interface{}) {
		logged += format
	}))
	pk := cipher.PubKey{}
	pk[0] = 0x02
	adj, err := h.BeforeDial(context.Background(), router.DialInfo{AppName: "x", PeerPK: pk})
	if err != nil {
		t.Fatalf("BeforeDial: %v", err)
	}
	if adj.MuxRoutes != 2 {
		t.Errorf("MuxRoutes=%d, want 2 (rest of adjustment must survive bad distribution)", adj.MuxRoutes)
	}
	if adj.Distribution.Mode != router.DistributionUnset {
		t.Errorf("Distribution.Mode=%v, want DistributionUnset (parse failed)", adj.Distribution.Mode)
	}
	if logged == "" {
		t.Errorf("expected the parse failure to be logged")
	}
}

func TestHook_SelectRoute_DistributionPropagates(t *testing.T) {
	// Script picks a candidate and ALSO returns distribution
	// based on its properties — the previously-impossible
	// dynamic-distribution case.
	src := `
def decide_route(ctx, candidates):
    if not candidates:
        return RouteSpec(mux=2)
    c = candidates[0]
    if "stcpr" in c.transport_kinds and "sudph" in c.transport_kinds:
        return RouteSpec(chosen=c, distribution="weighted: 3, 1")
    return RouteSpec(chosen=c, distribution="round-robin")
`
	hopA := "020000000000000000000000000000000000000000000000000000000000000001"
	prov := NewFakeProvider().
		SetGeo(hopA, "DE").
		SetKind(hopA, "stcpr")

	loader, err := NewLoader(src, WithProvider(prov))
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader, WithHookProvider(prov))
	pk := cipher.PubKey{}
	pk[0] = 0x02

	// Single-kind route → round-robin
	cands := []router.CandidateInfo{{Hops: []string{hopA}}}
	sel, err := h.SelectRoute(context.Background(), router.DialInfo{AppName: "x", PeerPK: pk}, cands, nil)
	if err != nil {
		t.Fatalf("SelectRoute single-kind: %v", err)
	}
	if sel.Distribution.Mode != router.DistributionRoundRobin {
		t.Errorf("single-kind: Distribution.Mode=%v, want DistributionRoundRobin", sel.Distribution.Mode)
	}
}

func TestHook_SelectRoute_BadDistributionLogsAndDefers(t *testing.T) {
	src := `
def decide_route(ctx, candidates):
    if not candidates:
        return RouteSpec()
    return RouteSpec(chosen=candidates[0], distribution="not-a-thing")
`
	hopA := "020000000000000000000000000000000000000000000000000000000000000001"
	prov := NewFakeProvider().SetGeo(hopA, "DE")

	loader, err := NewLoader(src, WithProvider(prov))
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	var logged string
	h := NewHook(loader, WithHookProvider(prov), WithHookLogger(func(f string, _ ...interface{}) {
		logged += f
	}))
	pk := cipher.PubKey{}
	pk[0] = 0x02
	cands := []router.CandidateInfo{{Hops: []string{hopA}}}
	sel, err := h.SelectRoute(context.Background(), router.DialInfo{AppName: "x", PeerPK: pk}, cands, nil)
	if err != nil {
		t.Fatalf("SelectRoute: %v", err)
	}
	if sel.Distribution.Mode != router.DistributionUnset {
		t.Errorf("bad descriptor in SelectRoute: Distribution.Mode=%v, want DistributionUnset", sel.Distribution.Mode)
	}
	if logged == "" {
		t.Errorf("expected the bad distribution to be logged")
	}
}

func TestHook_AsymmetricForwardReverseChosen(t *testing.T) {
	// Pick forward by hop[0]=="stcpr_hop"; pick reverse by
	// hop[0]=="sudph_hop". Script branches on transport_kinds.
	src := `
def decide_route(ctx, candidates):
    if not candidates:
        return RouteSpec()
    fwd = None
    for c in candidates:
        if "stcpr" in c.transport_kinds:
            fwd = c
            break
    rev = None
    for c in ctx.reverse_candidates:
        if "sudph" in c.transport_kinds:
            rev = c
            break
    return RouteSpec(chosen=fwd, reverse_chosen=rev)
`
	hopStcpr := "020000000000000000000000000000000000000000000000000000000000000011"
	hopSudph := "020000000000000000000000000000000000000000000000000000000000000022"

	prov := NewFakeProvider().
		SetKind(hopStcpr, "stcpr").
		SetKind(hopSudph, "sudph").
		SetGeo(hopStcpr, "DE").
		SetGeo(hopSudph, "ID")

	loader, err := NewLoader(src, WithProvider(prov))
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader, WithHookProvider(prov))
	pk := cipher.PubKey{}
	pk[0] = 0x02

	forward := []router.CandidateInfo{
		{Hops: []string{hopSudph}}, // first: sudph
		{Hops: []string{hopStcpr}}, // second: stcpr — script wants this for forward
	}
	reverse := []router.CandidateInfo{
		{Hops: []string{hopStcpr}}, // first: stcpr
		{Hops: []string{hopSudph}}, // second: sudph — script wants this for reverse
	}

	sel, err := h.SelectRoute(context.Background(),
		router.DialInfo{AppName: "vpn-client", PeerPK: pk}, forward, reverse)
	if err != nil {
		t.Fatalf("SelectRoute: %v", err)
	}
	if sel.Chosen != 1 {
		t.Errorf("forward Chosen=%d, want 1 (stcpr)", sel.Chosen)
	}
	if sel.ReverseChosen != 1 {
		t.Errorf("ReverseChosen=%d, want 1 (sudph)", sel.ReverseChosen)
	}
}

func TestHook_BeforeDial_AsymmetricMuxAndMinHops(t *testing.T) {
	src := `
def decide_route(ctx, candidates):
    return RouteSpec(forward_mux=1, reverse_mux=4, forward_min_hops=1, reverse_min_hops=3)
`
	loader, err := NewLoader(src)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader)
	pk := cipher.PubKey{}
	pk[0] = 0x02
	adj, err := h.BeforeDial(context.Background(), router.DialInfo{AppName: "x", PeerPK: pk})
	if err != nil {
		t.Fatalf("BeforeDial: %v", err)
	}
	if adj.ForwardMuxRoutes != 1 {
		t.Errorf("ForwardMuxRoutes=%d, want 1", adj.ForwardMuxRoutes)
	}
	if adj.ReverseMuxRoutes != 4 {
		t.Errorf("ReverseMuxRoutes=%d, want 4", adj.ReverseMuxRoutes)
	}
	if adj.ForwardMinHops != 1 {
		t.Errorf("ForwardMinHops=%d, want 1", adj.ForwardMinHops)
	}
	if adj.ReverseMinHops != 3 {
		t.Errorf("ReverseMinHops=%d, want 3", adj.ReverseMinHops)
	}
}

func TestHook_OnLegChange_RebalanceOnDrop(t *testing.T) {
	// Script re-balances weights when a leg drops: returns
	// "weighted: 1, 1" sized to the live leg count.
	src := `
def decide_route(ctx, candidates):
    return RouteSpec(mux=3, distribution="weighted: 3, 2, 1")

def on_leg_change(ctx, legs, change):
    alive = [l for l in legs if l.alive]
    n = len(alive)
    if n == 0:
        return RouteSpec()
    parts = []
    for i in range(n):
        parts.append("1")
    return RouteSpec(distribution = "weighted: " + ", ".join(parts))
`
	loader, err := NewLoader(src)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader)
	pk := cipher.PubKey{}
	pk[0] = 0x02
	info := router.DialInfo{AppName: "vpn-client", PeerPK: pk}

	// Simulate a drop event: 3 legs total, leg 1 is dead.
	legs := []router.LegInfo{
		{Index: 0, Kind: "stcpr", LatencyMs: 30, Alive: true},
		{Index: 1, Kind: "sudph", LatencyMs: 50, Alive: false},
		{Index: 2, Kind: "stcpr", LatencyMs: 80, Alive: true},
	}
	change := router.LegChange{Event: "dropped", LegIndex: 1}

	dist := h.OnLegChange(info, legs, change)
	if dist.Mode != router.DistributionWeighted {
		t.Errorf("Mode=%v, want DistributionWeighted", dist.Mode)
	}
	if len(dist.Weights) != 2 {
		t.Errorf("Weights len=%d, want 2 (for 2 alive legs)", len(dist.Weights))
	}
}

func TestHook_OnLegChange_ScriptWithoutFnReturnsUnset(t *testing.T) {
	// Most scripts don't define on_leg_change — the hook should
	// return the zero DistributionConfig so the route group
	// leaves its distribution unchanged.
	src := `
def decide_route(ctx, candidates):
    return RouteSpec()
`
	loader, err := NewLoader(src)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader)
	pk := cipher.PubKey{}
	pk[0] = 0x02
	dist := h.OnLegChange(
		router.DialInfo{AppName: "x", PeerPK: pk},
		[]router.LegInfo{{Index: 0, Kind: "stcpr", Alive: true}},
		router.LegChange{Event: "added", LegIndex: 0},
	)
	if dist.Mode != router.DistributionUnset {
		t.Errorf("Mode=%v, want DistributionUnset (script didn't define on_leg_change)", dist.Mode)
	}
}

func TestHook_OnLegChange_InactiveLoaderReturnsUnset(t *testing.T) {
	loader, err := NewLoader("") // noop mode
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader)
	pk := cipher.PubKey{}
	pk[0] = 0x02
	dist := h.OnLegChange(
		router.DialInfo{AppName: "x", PeerPK: pk},
		nil,
		router.LegChange{Event: "added", LegIndex: 0},
	)
	if dist.Mode != router.DistributionUnset {
		t.Errorf("Mode=%v, want DistributionUnset", dist.Mode)
	}
}

func TestHook_OnLegChange_AddedEvent(t *testing.T) {
	// Script handles "added" event: when a leg is added, switch
	// to round-robin from auto.
	src := `
def decide_route(ctx, candidates):
    return RouteSpec()

def on_leg_change(ctx, legs, change):
    if change.event == "added":
        return RouteSpec(distribution = "round-robin")
    return RouteSpec()
`
	loader, err := NewLoader(src)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader)
	pk := cipher.PubKey{}
	pk[0] = 0x02
	dist := h.OnLegChange(
		router.DialInfo{AppName: "x", PeerPK: pk},
		[]router.LegInfo{{Index: 0, Alive: true}, {Index: 1, Alive: true}},
		router.LegChange{Event: "added", LegIndex: 1},
	)
	if dist.Mode != router.DistributionRoundRobin {
		t.Errorf("added → Mode=%v, want DistributionRoundRobin", dist.Mode)
	}
	// Dropped event leaves it unset
	dist = h.OnLegChange(
		router.DialInfo{AppName: "x", PeerPK: pk},
		[]router.LegInfo{{Index: 0, Alive: true}},
		router.LegChange{Event: "dropped", LegIndex: 1},
	)
	if dist.Mode != router.DistributionUnset {
		t.Errorf("dropped → Mode=%v, want DistributionUnset", dist.Mode)
	}
}

func TestHook_BeforeDial_FallbackDirect(t *testing.T) {
	// Script asks for fallback="direct"; the router-side
	// applyAdjustment translates that into
	// UseExistingTpOnly=true + MinHops=1 + zeroed mux.
	src := `
def decide_route(ctx, candidates):
    return RouteSpec(fallback="direct")
`
	loader, err := NewLoader(src)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader)
	pk := cipher.PubKey{}
	pk[0] = 0x02
	adj, err := h.BeforeDial(context.Background(), router.DialInfo{AppName: "x", PeerPK: pk})
	if err != nil {
		t.Fatalf("BeforeDial: %v", err)
	}
	if adj.Fallback != "direct" {
		t.Errorf("Fallback=%q, want %q", adj.Fallback, "direct")
	}
}

func TestHook_BeforeDial_CLIOverridesVisible(t *testing.T) {
	// Script reads ctx.cli_overrides and chooses to honor or
	// override the operator's --routes flag.
	src := `
def decide_route(ctx, candidates):
    n = ctx.cli_overrides.get("mux_routes")
    if n == None:
        return RouteSpec(mux=2)
    # Operator passed --routes; honor it.
    return RouteSpec(mux=int(n))
`
	loader, err := NewLoader(src)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader)
	pk := cipher.PubKey{}
	pk[0] = 0x02
	// No CLI overrides → script's default of 2.
	adj, err := h.BeforeDial(context.Background(), router.DialInfo{AppName: "x", PeerPK: pk})
	if err != nil {
		t.Fatalf("BeforeDial: %v", err)
	}
	if adj.MuxRoutes != 2 {
		t.Errorf("no CLI: MuxRoutes=%d, want 2", adj.MuxRoutes)
	}
	// With CLI override → script honors it.
	adj, err = h.BeforeDial(context.Background(), router.DialInfo{
		AppName:      "x",
		PeerPK:       pk,
		CLIOverrides: map[string]string{"mux_routes": "8"},
	})
	if err != nil {
		t.Fatalf("BeforeDial: %v", err)
	}
	if adj.MuxRoutes != 8 {
		t.Errorf("with CLI: MuxRoutes=%d, want 8 (honored)", adj.MuxRoutes)
	}
}

func TestApplyAdjustment_FallbackDirect(t *testing.T) {
	t.Skip("router-package internal — see router_test")
}

func TestHook_BeforeDial_DirectDialCtxFields(t *testing.T) {
	// Script reads ctx.is_direct_dial + ctx.transport_kind and
	// branches: refuse vpn-client direct dials over dmsg, allow
	// everything else.
	src := `
def decide_route(ctx, candidates):
    if ctx.is_direct_dial and ctx.app == "vpn-client" and ctx.transport_kind == "dmsg":
        return RouteSpec(fallback="drop")
    return RouteSpec()
`
	loader, err := NewLoader(src)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader)
	pk := cipher.PubKey{}
	pk[0] = 0x02

	// vpn-client direct over dmsg → drop
	adj, err := h.BeforeDial(context.Background(), router.DialInfo{
		AppName:       "vpn-client",
		PeerPK:        pk,
		IsDirectDial:  true,
		TransportKind: "dmsg",
	})
	if err != nil {
		t.Fatalf("BeforeDial: %v", err)
	}
	if adj.Fallback != "drop" {
		t.Errorf("vpn+dmsg+direct: Fallback=%q, want drop", adj.Fallback)
	}

	// vpn-client direct over stcpr → allow
	adj, err = h.BeforeDial(context.Background(), router.DialInfo{
		AppName:       "vpn-client",
		PeerPK:        pk,
		IsDirectDial:  true,
		TransportKind: "stcpr",
	})
	if err != nil {
		t.Fatalf("BeforeDial: %v", err)
	}
	if adj.Fallback != "" {
		t.Errorf("vpn+stcpr+direct: Fallback=%q, want empty", adj.Fallback)
	}

	// vpn-client overlay (not direct) → allow even if kind="dmsg"
	adj, err = h.BeforeDial(context.Background(), router.DialInfo{
		AppName:       "vpn-client",
		PeerPK:        pk,
		IsDirectDial:  false,
		TransportKind: "dmsg",
	})
	if err != nil {
		t.Fatalf("BeforeDial: %v", err)
	}
	if adj.Fallback != "" {
		t.Errorf("vpn+dmsg+overlay: Fallback=%q, want empty (gate only fires for direct)", adj.Fallback)
	}
}

func TestHook_OnTick_RoundTripsToRouterRotationAction(t *testing.T) {
	// Policy returns a rotation cadence in decide_route + a
	// rotation action in on_tick that drops two legs and adds
	// one. Confirms the wire conversion through the Hook into
	// router.RotationAction with all fields populated.
	src := `
def decide_route(ctx, candidates):
    return RouteSpec(mux=4, rotation_interval_seconds=15)

def on_tick(ctx, legs):
    return Rotation(
        drop_legs=[0, 2],
        add_leg=True,
        exclude_hops=["0000000000000000000000000000000000000000000000000000000000000000ff"],
    )
`
	loader, err := NewLoader(src)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader)
	pk := cipher.PubKey{}
	pk[0] = 0x02
	info := router.DialInfo{AppName: "vpn-client", PeerPK: pk}

	// Confirm BeforeDial surfaces RotationIntervalSeconds on the
	// adjustment (the router uses this to bring up the rotation
	// loop).
	adj, err := h.BeforeDial(context.Background(), info)
	if err != nil {
		t.Fatalf("BeforeDial: %v", err)
	}
	if adj.RotationIntervalSeconds != 15 {
		t.Errorf("RotationIntervalSeconds=%d, want 15", adj.RotationIntervalSeconds)
	}

	// Confirm OnTick returns the policy's action through the
	// Hook into router-side types.
	legs := []router.LegInfo{
		{Index: 0, Kind: "stcpr", LatencyMs: 30, Alive: true},
		{Index: 1, Kind: "stcpr", LatencyMs: 40, Alive: true},
		{Index: 2, Kind: "sudph", LatencyMs: 60, Alive: true},
	}
	action := h.OnTick(info, legs)
	if !action.AddLeg {
		t.Errorf("AddLeg=false, want true")
	}
	if len(action.DropLegs) != 2 {
		t.Fatalf("DropLegs len=%d, want 2", len(action.DropLegs))
	}
	if action.DropLegs[0] != 0 || action.DropLegs[1] != 2 {
		t.Errorf("DropLegs=%v, want [0 2]", action.DropLegs)
	}
	if len(action.ExcludeHops) != 1 {
		t.Errorf("ExcludeHops len=%d, want 1", len(action.ExcludeHops))
	}
}

func TestHook_OnTick_ScriptWithoutFnReturnsZero(t *testing.T) {
	// Common case: policies that set rotation_interval_seconds
	// without defining on_tick. The Hook should return the zero-
	// value action so the route group doesn't try to mutate.
	src := `
def decide_route(ctx, candidates):
    return RouteSpec(mux=2, rotation_interval_seconds=60)
`
	loader, err := NewLoader(src)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck

	h := NewHook(loader)
	pk := cipher.PubKey{}
	pk[0] = 0x02
	info := router.DialInfo{AppName: "x", PeerPK: pk}
	action := h.OnTick(info, nil)
	if action.AddLeg || len(action.DropLegs) > 0 {
		t.Errorf("expected zero-value action, got %+v", action)
	}
}

func TestHook_BeforeDial_AvoidDirectImpliedByMinHops(t *testing.T) {
	// MinHops >= 2 should imply AvoidDirect — direct = 0 hops.
	cases := []struct {
		name string
		src  string
	}{
		{"min_hops>=2", `
def decide_route(ctx, candidates):
    return RouteSpec(min_hops=2)
`},
		{"forward_min_hops>=2", `
def decide_route(ctx, candidates):
    return RouteSpec(forward_min_hops=2)
`},
		{"reverse_min_hops>=2", `
def decide_route(ctx, candidates):
    return RouteSpec(reverse_min_hops=2)
`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			loader, err := NewLoader(c.src)
			if err != nil {
				t.Fatalf("NewLoader: %v", err)
			}
			defer loader.Close() //nolint:errcheck
			h := NewHook(loader)
			pk := cipher.PubKey{}
			pk[0] = 0x02
			info := router.DialInfo{AppName: "x", PeerPK: pk, IsDirectDial: true}
			adj, err := h.BeforeDial(context.Background(), info)
			if err != nil {
				t.Fatalf("BeforeDial: %v", err)
			}
			if !adj.AvoidDirect {
				t.Errorf("AvoidDirect=false, want true (implied by %s)", c.name)
			}
		})
	}
}

func TestHook_BeforeDial_AvoidDirectFalseWhenNoSignal(t *testing.T) {
	src := `
def decide_route(ctx, candidates):
    return RouteSpec(mux=2, min_hops=1)
`
	loader, err := NewLoader(src)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer loader.Close() //nolint:errcheck
	h := NewHook(loader)
	pk := cipher.PubKey{}
	pk[0] = 0x02
	info := router.DialInfo{AppName: "x", PeerPK: pk, IsDirectDial: true}
	adj, err := h.BeforeDial(context.Background(), info)
	if err != nil {
		t.Fatalf("BeforeDial: %v", err)
	}
	if adj.AvoidDirect {
		t.Errorf("AvoidDirect=true, want false (no signal)")
	}
}

func TestHook_OnTick_NoLoaderReturnsZero(t *testing.T) {
	// Hook with no engine registered should return zero-value
	// without panic.
	h := NewHook(nil)
	pk := cipher.PubKey{}
	pk[0] = 0x02
	action := h.OnTick(router.DialInfo{AppName: "x", PeerPK: pk}, nil)
	if action.AddLeg || len(action.DropLegs) > 0 {
		t.Errorf("expected zero-value action, got %+v", action)
	}
}
