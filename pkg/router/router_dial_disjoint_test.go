// router_dial_disjoint_test.go: unit tests for the disjoint-mux
// helpers added 2026-05-20. The mux-loop accumulator + the
// fetchBestRoutes post-filter both depend on these primitives;
// keeping them coverable in isolation lets the integration tests
// stay focused on dialer-level behavior.
package router

import (
	"testing"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

func mustPK(t *testing.T) cipher.PubKey {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return pk
}

func hop(from, to cipher.PubKey) routing.Hop {
	return routing.Hop{TpID: uuid.New(), From: from, To: to}
}

func TestPathTouchesIntermediate_EmptyAndDirect(t *testing.T) {
	src := mustPK(t)
	dst := mustPK(t)
	exclude := map[cipher.PubKey]struct{}{
		mustPK(t): {},
	}

	// Empty path: cannot touch anything.
	if pathTouchesIntermediate(nil, exclude) {
		t.Error("empty path: expected false, got true")
	}

	// Direct 1-hop path src->dst: only the endpoints exist, no
	// intermediates, so even a non-empty exclude set must not match.
	direct := []routing.Hop{hop(src, dst)}
	if pathTouchesIntermediate(direct, exclude) {
		t.Error("direct 1-hop: expected false (no intermediates), got true")
	}
}

func TestPathTouchesIntermediate_ExcludedIntermediateHit(t *testing.T) {
	src := mustPK(t)
	mid := mustPK(t)
	dst := mustPK(t)

	// 2-hop src->mid->dst with mid in the exclude set.
	path := []routing.Hop{hop(src, mid), hop(mid, dst)}
	exclude := map[cipher.PubKey]struct{}{mid: {}}

	if !pathTouchesIntermediate(path, exclude) {
		t.Error("2-hop with excluded intermediate: expected true, got false")
	}
}

func TestPathTouchesIntermediate_DestinationNotIntermediate(t *testing.T) {
	src := mustPK(t)
	mid := mustPK(t)
	dst := mustPK(t)

	// 2-hop path; if dst is in the exclude set, we should NOT flag
	// it — dst is the endpoint, not an intermediate.
	path := []routing.Hop{hop(src, mid), hop(mid, dst)}
	exclude := map[cipher.PubKey]struct{}{dst: {}}

	if pathTouchesIntermediate(path, exclude) {
		t.Error("dst in exclude set: expected false (dst is endpoint, not intermediate), got true")
	}
}

func TestPickDisjointPath_NoExcludeReturnsFirst(t *testing.T) {
	src := mustPK(t)
	mid1 := mustPK(t)
	mid2 := mustPK(t)
	dst := mustPK(t)

	fwd := [][]routing.Hop{
		{hop(src, mid1), hop(mid1, dst)},
		{hop(src, mid2), hop(mid2, dst)},
	}
	rev := [][]routing.Hop{
		{hop(dst, mid1), hop(mid1, src)},
		{hop(dst, mid2), hop(mid2, src)},
	}

	f, r, ok := pickDisjointPath(fwd, rev, nil)
	if !ok {
		t.Fatal("no-exclude: expected ok=true")
	}
	if f[0].To != mid1 {
		t.Errorf("no-exclude: expected first path (mid1), got %v", f[0].To)
	}
	_ = r
}

func TestPickDisjointPath_SkipsExcludedIntermediate(t *testing.T) {
	src := mustPK(t)
	mid1 := mustPK(t)
	mid2 := mustPK(t)
	mid3 := mustPK(t)
	dst := mustPK(t)

	// Three candidate routes: via mid1, mid2, mid3.
	// Exclude mid1 + mid2 → only mid3's path remains.
	fwd := [][]routing.Hop{
		{hop(src, mid1), hop(mid1, dst)},
		{hop(src, mid2), hop(mid2, dst)},
		{hop(src, mid3), hop(mid3, dst)},
	}
	rev := [][]routing.Hop{
		{hop(dst, mid1), hop(mid1, src)},
		{hop(dst, mid2), hop(mid2, src)},
		{hop(dst, mid3), hop(mid3, src)},
	}

	f, _, ok := pickDisjointPath(fwd, rev, []cipher.PubKey{mid1, mid2})
	if !ok {
		t.Fatal("exclude mid1+mid2 with mid3 available: expected ok=true")
	}
	if f[0].To != mid3 {
		t.Errorf("exclude mid1+mid2: expected mid3 path, got %v", f[0].To)
	}
}

func TestPickDisjointPath_AllExcluded(t *testing.T) {
	src := mustPK(t)
	mid1 := mustPK(t)
	mid2 := mustPK(t)
	dst := mustPK(t)

	fwd := [][]routing.Hop{
		{hop(src, mid1), hop(mid1, dst)},
		{hop(src, mid2), hop(mid2, dst)},
	}
	rev := [][]routing.Hop{
		{hop(dst, mid1), hop(mid1, src)},
		{hop(dst, mid2), hop(mid2, src)},
	}

	_, _, ok := pickDisjointPath(fwd, rev, []cipher.PubKey{mid1, mid2})
	if ok {
		t.Error("all paths excluded: expected ok=false")
	}
}

func TestPickDisjointPath_ReverseAlsoFiltered(t *testing.T) {
	src := mustPK(t)
	midF := mustPK(t)
	midR := mustPK(t)
	dst := mustPK(t)

	// Asymmetric path: forward via midF, reverse via midR.
	// If midR is excluded, the pair must be rejected even though
	// forward is clean.
	fwd := [][]routing.Hop{
		{hop(src, midF), hop(midF, dst)},
	}
	rev := [][]routing.Hop{
		{hop(dst, midR), hop(midR, src)},
	}

	_, _, ok := pickDisjointPath(fwd, rev, []cipher.PubKey{midR})
	if ok {
		t.Error("midR excluded but in reverse path: expected ok=false")
	}
}

func TestIntermediatesOfHops(t *testing.T) {
	src := mustPK(t)
	mid1 := mustPK(t)
	mid2 := mustPK(t)
	dst := mustPK(t)

	cases := []struct {
		name string
		hops []routing.Hop
		want []cipher.PubKey
	}{
		{
			name: "empty",
			hops: nil,
			want: nil,
		},
		{
			name: "direct 1-hop",
			hops: []routing.Hop{hop(src, dst)},
			want: nil,
		},
		{
			name: "2-hop",
			hops: []routing.Hop{hop(src, mid1), hop(mid1, dst)},
			want: []cipher.PubKey{mid1},
		},
		{
			name: "3-hop",
			hops: []routing.Hop{hop(src, mid1), hop(mid1, mid2), hop(mid2, dst)},
			want: []cipher.PubKey{mid1, mid2},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := intermediatesOfHops(c.hops, src, dst)
			if len(got) != len(c.want) {
				t.Fatalf("%s: want %d intermediates, got %d (%v)", c.name, len(c.want), len(got), got)
			}
			for i, pk := range got {
				if pk != c.want[i] {
					t.Errorf("%s: index %d want %v got %v", c.name, i, c.want[i], pk)
				}
			}
		})
	}
}
