// Package router pkg/router/router_dial_direct_gate_test.go c2-net-routing
package router

import "testing"

// A per-dial min-hops above 1 must keep the dial off the direct transport
// even when the visor floor is 1 and a direct transport exists.
func TestDirectRouteGateHonorsPerDialMinHops(t *testing.T) {
	cases := []struct {
		name               string
		base               uint16
		opts               *DialOptions
		wantCreate, wantGo bool
	}{
		{"no opts", 1, nil, true, true},
		{"unset", 1, &DialOptions{}, true, true},
		{"one hop", 1, &DialOptions{MinHops: 1}, true, true},
		{"two hops", 1, &DialOptions{MinHops: 2}, false, false},
		{"reverse two hops", 1, &DialOptions{ReverseMinHops: 2}, true, false},
		{"forward two hops", 1, &DialOptions{ForwardMinHops: 2}, true, false},
		{"both directions two hops", 1, &DialOptions{ForwardMinHops: 2, ReverseMinHops: 2}, false, false},
		{"visor floor two", 2, &DialOptions{}, false, false},
	}
	for _, c := range cases {
		if got := wantsDirectTransport(c.base, c.opts); got != c.wantCreate {
			t.Errorf("%s: wantsDirectTransport = %v, want %v", c.name, got, c.wantCreate)
		}
		if got := directRouteAllowed(c.base, c.opts); got != c.wantGo {
			t.Errorf("%s: directRouteAllowed = %v, want %v", c.name, got, c.wantGo)
		}
	}
}
