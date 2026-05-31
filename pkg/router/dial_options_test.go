// Package router pkg/router/dial_options_test.go — unit tests for the
// per-direction resolver helpers on DialOptions added for asymmetric
// routing (operator framing: bandwidth-asymmetric workloads like HTTP
// GET should be able to use direct upstream + multi-hop downstream).
package router

import (
	"testing"
)

func TestDialOptions_EffectiveMinHops(t *testing.T) {
	cases := []struct {
		name    string
		opts    *DialOptions
		forward bool
		want    int
	}{
		{"nil opts forward", nil, true, 0},
		{"nil opts reverse", nil, false, 0},
		{"symmetric only forward", &DialOptions{MinHops: 2}, true, 2},
		{"symmetric only reverse", &DialOptions{MinHops: 2}, false, 2},
		{"per-direction wins forward", &DialOptions{MinHops: 2, ForwardMinHops: 4}, true, 4},
		{"per-direction wins reverse", &DialOptions{MinHops: 2, ReverseMinHops: 4}, false, 4},
		{"per-direction does NOT affect other direction", &DialOptions{MinHops: 2, ForwardMinHops: 4}, false, 2},
		{"per-direction does NOT affect other direction rev", &DialOptions{MinHops: 2, ReverseMinHops: 4}, true, 2},
		{"asymmetric: forward=0 reverse=2", &DialOptions{ForwardMinHops: 0, ReverseMinHops: 2}, true, 0},
		{"asymmetric: forward=0 reverse=2 rev", &DialOptions{ForwardMinHops: 0, ReverseMinHops: 2}, false, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.opts.EffectiveMinHops(c.forward)
			if got != c.want {
				t.Errorf("EffectiveMinHops(%v) = %d, want %d", c.forward, got, c.want)
			}
		})
	}
}

func TestDialOptions_EffectiveMuxRoutes(t *testing.T) {
	cases := []struct {
		name    string
		opts    *DialOptions
		forward bool
		want    int
	}{
		{"nil opts forward", nil, true, 0},
		{"nil opts reverse", nil, false, 0},
		{"symmetric only forward", &DialOptions{MuxRoutes: 4}, true, 4},
		{"symmetric only reverse", &DialOptions{MuxRoutes: 4}, false, 4},
		{"per-direction wins forward", &DialOptions{MuxRoutes: 4, ForwardMuxRoutes: 1}, true, 1},
		{"per-direction wins reverse", &DialOptions{MuxRoutes: 4, ReverseMuxRoutes: 8}, false, 8},
		{"per-direction does NOT affect other forward", &DialOptions{MuxRoutes: 4, ReverseMuxRoutes: 8}, true, 4},
		{"per-direction does NOT affect other reverse", &DialOptions{MuxRoutes: 4, ForwardMuxRoutes: 1}, false, 4},
		// Canonical asymmetric download shape: fwd=1, rev=N.
		{"asymmetric: fwd=1 rev=4 fwd", &DialOptions{ForwardMuxRoutes: 1, ReverseMuxRoutes: 4}, true, 1},
		{"asymmetric: fwd=1 rev=4 rev", &DialOptions{ForwardMuxRoutes: 1, ReverseMuxRoutes: 4}, false, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.opts.EffectiveMuxRoutes(c.forward)
			if got != c.want {
				t.Errorf("EffectiveMuxRoutes(%v) = %d, want %d", c.forward, got, c.want)
			}
		})
	}
}

func TestDialOptions_AnyMinHopsConstraint(t *testing.T) {
	cases := []struct {
		name string
		opts *DialOptions
		want bool
	}{
		{"nil", nil, false},
		{"all zero", &DialOptions{}, false},
		{"symmetric 1", &DialOptions{MinHops: 1}, false},
		{"symmetric 2", &DialOptions{MinHops: 2}, true},
		{"per-direction fwd 2", &DialOptions{ForwardMinHops: 2}, true},
		{"per-direction rev 2", &DialOptions{ReverseMinHops: 2}, true},
		// The asymmetric "1 forward + multi-hop reverse" canonical case:
		// MinHops untouched, ReverseMinHops bumped above 1. The direct-
		// transport downgrade in DialRoutes must NOT fire here.
		{"asymmetric reverse-only", &DialOptions{ReverseMinHops: 2}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.opts.AnyMinHopsConstraint()
			if got != c.want {
				t.Errorf("AnyMinHopsConstraint() = %v, want %v", got, c.want)
			}
		})
	}
}
