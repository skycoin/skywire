package proxystatus

import (
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/bitree"
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

	// Single stream → no stream chrome: legs hang directly off root, unbanded.
	one := RouteTree(Snapshot{Tunnels: []Tunnel{{Index: 0, Legs: []Leg{leg(0, true), leg(1, false)}}}})
	if len(one.Right) != 2 {
		t.Fatalf("single stream: root has %d children, want 2 legs", len(one.Right))
	}
	if strings.Contains(one.Right[0].Left[0].Label, StreamBandGlyph) {
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
