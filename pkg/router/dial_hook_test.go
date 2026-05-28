package router

import (
	"testing"
)

func TestApplyAdjustment_FallbackDirect(t *testing.T) {
	opts := &DialOptions{
		MuxRoutes:        4,
		ForwardMuxRoutes: 2,
		ReverseMuxRoutes: 2,
		MinHops:          3,
	}
	adj := DialAdjustment{Fallback: "direct"}
	if err := applyAdjustment(opts, adj); err != nil {
		t.Fatalf("applyAdjustment: %v", err)
	}
	if !opts.UseExistingTpOnly {
		t.Errorf("UseExistingTpOnly=false, want true (direct fallback should force it)")
	}
	if opts.MinHops != 1 {
		t.Errorf("MinHops=%d, want 1", opts.MinHops)
	}
	if opts.MuxRoutes != 0 {
		t.Errorf("MuxRoutes=%d, want 0 (mux is meaningless for direct dial)", opts.MuxRoutes)
	}
	if opts.ForwardMuxRoutes != 0 || opts.ReverseMuxRoutes != 0 {
		t.Errorf("Forward/Reverse mux not zeroed: %d/%d", opts.ForwardMuxRoutes, opts.ReverseMuxRoutes)
	}
}

func TestApplyAdjustment_PolicyOverridesCLI(t *testing.T) {
	// CLI set mux=2, policy sets mux=4 — policy wins.
	opts := &DialOptions{MuxRoutes: 2, MinHops: 1}
	adj := DialAdjustment{MuxRoutes: 4, MinHops: 3}
	if err := applyAdjustment(opts, adj); err != nil {
		t.Fatalf("applyAdjustment: %v", err)
	}
	if opts.MuxRoutes != 4 {
		t.Errorf("MuxRoutes=%d, want 4 (policy wins)", opts.MuxRoutes)
	}
	if opts.MinHops != 3 {
		t.Errorf("MinHops=%d, want 3 (policy wins)", opts.MinHops)
	}
}

func TestApplyAdjustment_PolicyDefersToCLI(t *testing.T) {
	// CLI set mux=8, policy returns zero (silent) → CLI sticks.
	opts := &DialOptions{MuxRoutes: 8, MinHops: 2}
	adj := DialAdjustment{} // all zero
	if err := applyAdjustment(opts, adj); err != nil {
		t.Fatalf("applyAdjustment: %v", err)
	}
	if opts.MuxRoutes != 8 {
		t.Errorf("MuxRoutes=%d, want 8 (policy silent → CLI wins)", opts.MuxRoutes)
	}
	if opts.MinHops != 2 {
		t.Errorf("MinHops=%d, want 2 (policy silent → CLI wins)", opts.MinHops)
	}
}

func TestBuildCLIOverrides_OnlyNonZeroSurfaces(t *testing.T) {
	opts := &DialOptions{
		MuxRoutes:        4,
		ForwardMuxRoutes: 2,
		MinHops:          0, // zero — must not surface
	}
	cli := buildCLIOverrides(opts)
	if cli["mux_routes"] != "4" {
		t.Errorf("mux_routes=%q, want \"4\"", cli["mux_routes"])
	}
	if cli["forward_mux_routes"] != "2" {
		t.Errorf("forward_mux_routes=%q, want \"2\"", cli["forward_mux_routes"])
	}
	if _, ok := cli["min_hops"]; ok {
		t.Errorf("min_hops surfaced despite being zero")
	}
}

func TestBuildCLIOverrides_NilOptsReturnsNil(t *testing.T) {
	if cli := buildCLIOverrides(nil); cli != nil {
		t.Errorf("buildCLIOverrides(nil) = %v, want nil", cli)
	}
}

func TestBuildCLIOverrides_AllZeroReturnsNil(t *testing.T) {
	if cli := buildCLIOverrides(&DialOptions{}); cli != nil {
		t.Errorf("buildCLIOverrides({}) = %v, want nil (no defaults to surface)", cli)
	}
}
