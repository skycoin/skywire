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
	sel, err := h.SelectRoute(context.Background(), router.DialInfo{AppName: "skychat", PeerPK: pk}, cands)
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
		[]router.CandidateInfo{{Hops: []string{hopA}}})
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
