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
