// Package store pkg/deployment/rf/store/landmark_test.go c2-net-routing
package store

import (
	"context"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// pkOf builds a distinct deterministic pubkey for index i (non-zero map key).
func pkOf(i int) cipher.PubKey {
	var pk cipher.PubKey
	pk[0] = 0x02
	pk[1] = byte(i)
	pk[2] = byte(i >> 8)
	pk[3] = byte(i >> 16)
	return pk
}

// buildHubGraph makes a hub-and-leaf mesh: `hubs` fully-interconnected hub nodes
// (a clique — the combinatorial paths through it are what make an exhaustive BFS
// expensive) plus `leaves` leaves, each attached to `linksPerLeaf` distinct hubs
// (deterministic). A far leaf->leaf pair must transit the hub core, exactly the
// shape landmark composition targets. All edges are STCPR with distinct tpids.
func buildHubGraph(tb testing.TB, hubs, leaves, linksPerLeaf int) (*Graph, []cipher.PubKey, []cipher.PubKey) {
	tb.Helper()
	m := newMockStore()
	hubPK := make([]cipher.PubKey, hubs)
	for h := 0; h < hubs; h++ {
		hubPK[h] = pkOf(h)
	}
	for a := 0; a < hubs; a++ {
		for b := a + 1; b < hubs; b++ {
			m.saveEntryLat(hubPK[a], hubPK[b], float64(1+(a+b)%5))
		}
	}
	leafPK := make([]cipher.PubKey, leaves)
	for l := 0; l < leaves; l++ {
		leafPK[l] = pkOf(1000 + l)
		for k := 0; k < linksPerLeaf; k++ {
			h := (l*7 + k*3) % hubs
			m.saveEntryLat(leafPK[l], hubPK[h], float64(2+(l+k)%7))
		}
	}
	g, err := NewGraph(context.Background(), m, hubPK[0])
	if err != nil {
		tb.Fatal(err)
	}
	return g, hubPK, leafPK
}

// validateRoute asserts r is a well-formed src->dst route: correct endpoints,
// chained hops, loop-free, within maxLen, every hop live in g.
func validateRoute(t *testing.T, g *Graph, r routing.Route, src, dst cipher.PubKey, maxLen int) {
	t.Helper()
	if len(r.Hops) == 0 {
		t.Fatalf("empty route")
	}
	if len(r.Hops) > maxLen {
		t.Fatalf("route length %d exceeds maxLen %d", len(r.Hops), maxLen)
	}
	if r.Hops[0].From != src {
		t.Fatalf("route does not start at src")
	}
	if r.Hops[len(r.Hops)-1].To != dst {
		t.Fatalf("route does not end at dst")
	}
	seen := map[cipher.PubKey]struct{}{r.Hops[0].From: {}}
	for i, h := range r.Hops {
		if i > 0 && r.Hops[i-1].To != h.From {
			t.Fatalf("broken chain at hop %d", i)
		}
		if _, dup := seen[h.To]; dup {
			t.Fatalf("loop: node repeats at hop %d", i)
		}
		seen[h.To] = struct{}{}
		v, ok := g.graph[h.From]
		if !ok {
			t.Fatalf("hop %d from-vertex missing", i)
		}
		conn, ok := v.connections[h.To]
		if !ok || conn.ID != h.TpID {
			t.Fatalf("hop %d references a transport not live in the graph", i)
		}
	}
}

// bfs/landmark route fetch through computeRouteWeighted (skips the memo, so the
// flag toggle takes effect on each call; the lazily-built tables are
// flag-independent). setLandmark flips the package gate around the call.
func routesVia(t *testing.T, g *Graph, on bool, src, dst cipher.PubKey, maxLen, number int) ([]routing.Route, error) {
	t.Helper()
	prev := landmarkRoutingEnabled
	landmarkRoutingEnabled = on
	defer func() { landmarkRoutingEnabled = prev }()
	return g.computeRouteWeighted(context.Background(), src, dst, 0, maxLen, number, true)
}

// TestLandmarkComposeValidAndShadowsBFS: every landmark route is valid, and
// wherever the exhaustive BFS finds a route the landmark path also finds one.
func TestLandmarkComposeValidAndShadowsBFS(t *testing.T) {
	g, hubPK, leafPK := buildHubGraph(t, 8, 60, 3)
	const maxLen, number = 8, 3

	pairs := [][2]cipher.PubKey{
		{leafPK[0], leafPK[30]},
		{leafPK[5], leafPK[45]},
		{leafPK[12], leafPK[58]},
		{leafPK[1], hubPK[4]},
		{hubPK[2], leafPK[20]},
	}
	for _, p := range pairs {
		src, dst := p[0], p[1]
		bfsRoutes, bfsErr := routesVia(t, g, false, src, dst, maxLen, number)
		lmRoutes, lmErr := routesVia(t, g, true, src, dst, maxLen, number)

		if bfsErr == nil && len(bfsRoutes) > 0 {
			if lmErr != nil || len(lmRoutes) == 0 {
				t.Fatalf("pair %v: BFS found %d routes, landmark found none (err=%v)", src[:2], len(bfsRoutes), lmErr)
			}
		}
		for _, r := range lmRoutes {
			validateRoute(t, g, r, src, dst, maxLen)
		}
	}
}

// TestLandmarkDisjointLegs: a far pair's two best legs are not fully overlapping
// in intermediates — distinct-hub composition gives the mux disjoint legs.
func TestLandmarkDisjointLegs(t *testing.T) {
	g, _, leafPK := buildHubGraph(t, 8, 60, 3)
	routes, err := routesVia(t, g, true, leafPK[3], leafPK[40], 8, 3)
	if err != nil || len(routes) < 2 {
		t.Skipf("need >=2 routes (got %d, err=%v)", len(routes), err)
	}
	inter := func(r routing.Route) map[cipher.PubKey]struct{} {
		s := map[cipher.PubKey]struct{}{}
		for i, h := range r.Hops {
			if i < len(r.Hops)-1 {
				s[h.To] = struct{}{}
			}
		}
		return s
	}
	a, b := inter(routes[0]), inter(routes[1])
	overlap := 0
	for pk := range a {
		if _, ok := b[pk]; ok {
			overlap++
		}
	}
	if len(a) > 0 && overlap == len(a) {
		t.Errorf("two legs share all intermediates — not disjoint")
	}
}

// TestLandmarkClosePairParity: a close pair keeps its optimal short route length
// under the landmark hybrid (budget-BFS-first must not lengthen near pairs).
func TestLandmarkClosePairParity(t *testing.T) {
	g, hubPK, leafPK := buildHubGraph(t, 8, 60, 3)
	src, dst := leafPK[0], hubPK[0] // leaf 0 attaches to hub 0 directly
	bfs, err := routesVia(t, g, false, src, dst, 8, 3)
	if err != nil || len(bfs) == 0 {
		t.Fatalf("BFS: %v", err)
	}
	lm, err := routesVia(t, g, true, src, dst, 8, 3)
	if err != nil || len(lm) == 0 {
		t.Fatalf("landmark: %v", err)
	}
	if len(lm[0].Hops) != len(bfs[0].Hops) {
		t.Errorf("close-pair best-route length differs: BFS=%d landmark=%d", len(bfs[0].Hops), len(lm[0].Hops))
	}
}

// BenchmarkRouteFarPair_FullBFS vs _Landmark: a far (src,dst) on a dense layered
// mesh — the shape where the exhaustive BFS is expensive. Landmark serves it via
// shallow-BFS-fast-fail + hub composition. Tables are pre-built (amortized once
// per graph in production) so the benchmark measures the SERVE cost.
func BenchmarkRouteFarPair_FullBFS(b *testing.B) {
	g, src, dst := buildDenseGraph(b, 6, 6)
	landmarkRoutingEnabled = false
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = g.computeRouteWeighted(context.Background(), src, dst, 0, 8, 3, true)
	}
}

func BenchmarkRouteFarPair_Landmark(b *testing.B) {
	g, src, dst := buildDenseGraph(b, 6, 6)
	landmarkRoutingEnabled = true
	g.ensureLandmarks() // amortized once per graph; measure serve only
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = g.computeRouteWeighted(context.Background(), src, dst, 0, 8, 3, true)
	}
}
