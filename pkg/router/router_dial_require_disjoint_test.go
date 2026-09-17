// router_dial_require_disjoint_test.go: the exhaustion half of the diversify
// dial. DialOptions.RequireDisjointFirstHop turns "prefer a free first hop"
// into "fail if none is free", and ErrNoDisjointFirstHop is how a caller
// growing a pool of sibling tunnels learns the topology has nothing left to
// offer. These pin the sentinel's identity and the candidate-exhaustion
// condition that raises it.
package router

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// A candidate set whose every first hop is already claimed must come back
// empty — that empty set is precisely what fetchBestRoutes reads as exhaustion.
func TestFreeFirstHops_AllClaimedIsExhaustion(t *testing.T) {
	src, dst := mustPK(t), mustPK(t)
	mid1, mid2 := mustPK(t), mustPK(t)

	a := []routing.Hop{hop(src, mid1), hop(mid1, dst)}
	b := []routing.Hop{hop(src, mid2), hop(mid2, dst)}
	cands := [][]routing.Hop{a, b}

	r := &router{}
	opts := &DialOptions{
		DiversifyTransports:     true,
		RequireDisjointFirstHop: true,
		ExcludeTransportIDs:     []uuid.UUID{a[0].TpID, b[0].TpID},
	}
	if got := r.freeFirstHops(cands, opts); len(got) != 0 {
		t.Fatalf("every first hop claimed: want 0 free candidates, got %d", len(got))
	}
	// And the single-path form agrees, which is what the pre-setup gate in
	// DialRoutes uses on the route it is about to build.
	if !r.firstHopExcluded(a, opts) {
		t.Error("firstHopExcluded: claimed first hop reported free")
	}
}

// One free first hop among many claimed is NOT exhaustion: the pool keeps
// filling. This is the case that must never raise the error.
func TestFreeFirstHops_OneFreeIsNotExhaustion(t *testing.T) {
	src, dst := mustPK(t), mustPK(t)
	mid1, mid2 := mustPK(t), mustPK(t)

	claimed := []routing.Hop{hop(src, mid1), hop(mid1, dst)}
	free := []routing.Hop{hop(src, mid2), hop(mid2, dst)}

	r := &router{}
	opts := &DialOptions{
		DiversifyTransports:     true,
		RequireDisjointFirstHop: true,
		ExcludeTransportIDs:     []uuid.UUID{claimed[0].TpID},
	}
	got := r.freeFirstHops([][]routing.Hop{claimed, free}, opts)
	if len(got) != 1 || got[0][0].TpID != free[0].TpID {
		t.Fatalf("want the one unclaimed candidate, got %d candidate(s)", len(got))
	}
	if r.firstHopExcluded(free, opts) {
		t.Error("firstHopExcluded: unclaimed first hop reported taken")
	}
}

// A first-hop PEER a sibling already leaves over is exhausted too, even when
// its transport ID is different — one peer answers on stcpr, squicr and sudph
// alike, and all three ride the same link.
func TestFreeFirstHops_PeerTwinIsClaimed(t *testing.T) {
	src, dst := mustPK(t), mustPK(t)
	peer := mustPK(t)

	// Two distinct transports (distinct TpIDs) to the SAME first-hop peer.
	twinA := []routing.Hop{hop(src, peer), hop(peer, dst)}
	twinB := []routing.Hop{hop(src, peer), hop(peer, dst)}

	r := &router{}
	opts := &DialOptions{
		DiversifyTransports:     true,
		RequireDisjointFirstHop: true,
		ExcludeFirstHopPeers:    []cipher.PubKey{peer},
	}
	if got := r.freeFirstHops([][]routing.Hop{twinA, twinB}, opts); len(got) != 0 {
		t.Fatalf("both candidates leave over the claimed peer: want 0 free, got %d", len(got))
	}
}

// The wrapped error must stay matchable by errors.Is — the whole point of the
// sentinel is that the skysocks pool can tell exhaustion from a dial failure.
func TestNoDisjointFirstHopErr_IsMatchable(t *testing.T) {
	dst := mustPK(t)
	err := noDisjointFirstHopErr(dst, 7)
	if !errors.Is(err, ErrNoDisjointFirstHop) {
		t.Fatalf("errors.Is(%v, ErrNoDisjointFirstHop) = false", err)
	}
	if errors.Is(err, ErrNoRouteFound) {
		t.Error("exhaustion must NOT masquerade as ErrNoRouteFound: a caller retries that one")
	}
	// The numbers a reader needs are in the message.
	if msg := err.Error(); !strings.Contains(msg, "7 candidate") || !strings.Contains(msg, dst.String()) {
		t.Errorf("message lost its context: %q", msg)
	}
}
