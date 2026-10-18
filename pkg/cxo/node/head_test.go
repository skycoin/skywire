package node

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cxo/skyobject"
)

// newTestFillHead wires up the minimal embedding chain required for
// fillHead.node() to resolve to the supplied *Node:
//
//	fillHead → embedded *nodeHead → nodeHead.n (*nodeFeed)
//	         → nodeFeed.fs (*nodeFeeds) → nodeFeeds.n (*Node).
//
// Only maxParallel() is exercised so the surrounding fields stay zero.
func newTestFillHead(n *Node) *fillHead {
	feeds := &nodeFeeds{n: n}
	feed := &nodeFeed{fs: feeds}
	return &fillHead{nodeHead: &nodeHead{n: feed}}
}

// TestFillHead_maxParallel_FallbackUsesDocumentedDefault verifies the
// safety-net fallback when (*Node).maxFillingParallel is 0 (Config
// constructed bare, not via NewConfig). It must use the documented
// skyobject.MaxFillingParallel constant (= 10) rather than the
// previous magic 1024 that over-sized Filler.limit + Filler.rq per
// Filler.
func TestFillHead_maxParallel_FallbackUsesDocumentedDefault(t *testing.T) {
	fh := newTestFillHead(&Node{maxFillingParallel: 0})
	if mp := fh.maxParallel(); mp != skyobject.MaxFillingParallel {
		t.Fatalf("maxParallel() fallback = %d, want skyobject.MaxFillingParallel = %d",
			mp, skyobject.MaxFillingParallel)
	}
}

// TestFillHead_maxParallel_ExplicitOverride confirms the operator-set
// value wins over the fallback when Config.MaxFillingParallel is
// non-zero — exact value pass-through, no clamping.
func TestFillHead_maxParallel_ExplicitOverride(t *testing.T) {
	for _, want := range []int{1, 5, 64, 1024, 4096} {
		fh := newTestFillHead(&Node{maxFillingParallel: want})
		if mp := fh.maxParallel(); mp != want {
			t.Fatalf("maxParallel() with operator-set %d = %d, want %d", want, mp, want)
		}
	}
}

// TestFillHead_maxParallel_NegativeFallsBack covers the boundary where
// the operator-set value is <= 0 (negative or zero); both should fall
// back to the documented default.
func TestFillHead_maxParallel_NegativeFallsBack(t *testing.T) {
	for _, in := range []int{-1, -100, 0} {
		fh := newTestFillHead(&Node{maxFillingParallel: in})
		if mp := fh.maxParallel(); mp != skyobject.MaxFillingParallel {
			t.Fatalf("maxParallel() with %d fell back to %d, want %d",
				in, mp, skyobject.MaxFillingParallel)
		}
	}
}

// TestKnownRoots_moveForward_KeepsNewerRoots pins the bookkeeping after a
// fill: the Roots up to the filled one are forgotten, and a newer Root that
// arrived meanwhile must still name the connection that has it. Dropping it
// left the next fill with no connection ("no connections to fill from").
func TestKnownRoots_moveForward_KeepsNewerRoots(t *testing.T) {
	a, b := &Conn{}, &Conn{}
	k := knownRoots{}
	for _, seq := range []uint64{2, 0, 1} {
		k.addKnown(a, seq)
	}
	k.addKnown(b, 0)

	k.moveForward(1) // Root 0 filled

	if got := k[a]; len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("known of a = %v, want [1 2]", got)
	}
	if got := k[b]; len(got) != 0 {
		t.Fatalf("known of b = %v, want none", got)
	}
	if l := k.buildConnsList(1); l.Len() != 1 || l.Front().Value.(*Conn) != a {
		t.Fatalf("connections to fill Root 1 from = %d, want only a", l.Len())
	}
}
