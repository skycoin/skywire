package proxystatus

import (
	"strings"
	"testing"

	"github.com/0magnet/bitree"
)

// TestRouteTreeTwoLevel: with MORE THAN ONE stream the tree exposes both
// multiplexing layers — a stream-boundary header node per tunnel followed by
// that tunnel's legs (top-level so their left summaries render), each leg banded
// with its stream tag. A single stream (or the flat Legs fallback) renders clean
// with no stream chrome.
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
	// Two stream headers + (2+3) legs = 7 top-level spine routes.
	if len(root.Right) != 7 {
		t.Fatalf("root has %d spine children, want 7 (2 headers + 5 legs)", len(root.Right))
	}
	// The stream-boundary headers carry the marker and differ by index; they lead
	// each stream's legs.
	hdr := func(n *bitree.Node) bool { return strings.HasPrefix(n.Label, StreamHeaderGlyph) }
	if !hdr(root.Right[0]) || !hdr(root.Right[3]) {
		t.Error("stream headers should lead each stream's legs")
	}
	if root.Right[0].Label == root.Right[3].Label {
		t.Error("stream header labels should differ by index")
	}
	// Legs are top-level (so their left summary renders) and carry their stream band.
	if len(root.Right[1].Left) == 0 {
		t.Error("a leg node should carry a left summary")
	}
	if !strings.Contains(root.Right[1].Left[0].Label, StreamBandGlyph+"s0") {
		t.Errorf("stream-0 leg summary missing its band: %q", root.Right[1].Left[0].Label)
	}
	if !strings.Contains(root.Right[4].Left[0].Label, StreamBandGlyph+"s1") {
		t.Errorf("stream-1 leg summary missing its band: %q", root.Right[4].Left[0].Label)
	}

	// Single stream → a thin stream header, then its legs as SIBLING top-level
	// spine routes (so their rich left summary renders), UNBANDED (nothing to tell
	// apart with one stream). This is the #4313 regression fix: a lone stream must
	// still show every leg's R[n] / bandwidth / branches, not just a bare header.
	one := RouteTree(Snapshot{Tunnels: []Tunnel{{Index: 0, Legs: []Leg{leg(0, true), leg(1, false)}}}})
	if len(one.Right) != 3 {
		t.Fatalf("single stream: root has %d children, want 3 (1 header + 2 legs)", len(one.Right))
	}
	if !strings.HasPrefix(one.Right[0].Label, StreamHeaderGlyph) {
		t.Error("single-stream: first spine child should be the stream header")
	}
	if len(one.Right[1].Left) == 0 || len(one.Right[2].Left) == 0 {
		t.Error("single-stream legs must be top-level with a left summary (R[n]/bandwidth)")
	}
	if strings.Contains(one.Right[1].Left[0].Label, StreamBandGlyph) {
		t.Error("single-stream leg should not be banded")
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

// TestLegOrderStableUnderIndexChurn verifies that legs render in a stable order
// keyed on identity (intermediate PK), not on their churning Index — so the tree
// does not reshuffle its branches as the warm-standby pool adds/drops legs.
func TestLegOrderStableUnderIndexChurn(t *testing.T) {
	mk := func(idx int, remote string) Leg {
		return Leg{Index: idx, RemotePK: remote, TransportID: "tp-" + remote, Alive: true, Standby: true,
			Hops: []Hop{{From: "src", To: remote, TpType: "stcpr"}}}
	}
	// Same three routes (by intermediate), presented with DIFFERENT indices/order.
	a := legNodesForStream([]Leg{mk(0, "pkC"), mk(1, "pkA"), mk(2, "pkB")}, -1)
	b := legNodesForStream([]Leg{mk(7, "pkB"), mk(3, "pkC"), mk(9, "pkA")}, -1)
	if len(a) != len(b) || len(a) == 0 {
		t.Fatalf("leg node counts differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Label != b[i].Label {
			t.Fatalf("leg order not stable under index churn at %d: %q vs %q", i, a[i].Label, b[i].Label)
		}
	}
}
