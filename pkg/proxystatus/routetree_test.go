package proxystatus

import "testing"

// TestRouteTreeTwoLevel: with Tunnels populated the tree nests root → tunnel
// (stream) → legs (packet); the flat Legs fallback still applies when Tunnels is
// empty.
func TestRouteTreeTwoLevel(t *testing.T) {
	leg := func(idx int, direct bool) Leg {
		return Leg{Index: idx, Alive: true, Direct: direct, GoodputDownBps: 1000,
			Hops: []Hop{{From: "srcpk", To: "dstpk"}}}
	}
	snap := Snapshot{
		Tunnels: []Tunnel{
			{Index: 0, Legs: []Leg{leg(0, true), leg(1, false)}},
			{Index: 1, Legs: []Leg{leg(0, false), leg(1, false), leg(2, false)}},
		},
	}
	root := RouteTree(snap)
	if root == nil {
		t.Fatal("nil root")
	}
	if len(root.Right) != 2 {
		t.Fatalf("root has %d tunnel children, want 2", len(root.Right))
	}
	// Tunnel 0 has 2 legs, tunnel 1 has 3 — the packet-level spread under each.
	if got := len(root.Right[0].Right); got != 2 {
		t.Errorf("tunnel 0 has %d legs, want 2", got)
	}
	if got := len(root.Right[1].Right); got != 3 {
		t.Errorf("tunnel 1 has %d legs, want 3", got)
	}
	if root.Right[0].Label == root.Right[1].Label {
		t.Error("tunnel labels should differ by index")
	}

	// Empty Tunnels → flat fallback: legs hang directly off root.
	flat := RouteTree(Snapshot{Legs: []Leg{leg(0, true), leg(1, false)}})
	if len(flat.Right) != 2 {
		t.Fatalf("flat fallback: root has %d leg children, want 2", len(flat.Right))
	}
	if len(flat.Right[0].Right) != 0 {
		t.Error("flat fallback should not nest a tunnel level")
	}
}
