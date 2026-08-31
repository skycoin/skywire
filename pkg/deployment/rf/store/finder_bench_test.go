package store

import (
	"context"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// buildDenseGraph makes a layered mesh: `layers` layers of `width` nodes each,
// every node in layer i fully connected to every node in layer i+1 — a dense
// multi-path graph that reproduces the BFS branching blow-up.
func buildDenseGraph(tb testing.TB, layers, width int) (*Graph, cipher.PubKey, cipher.PubKey) {
	tb.Helper()
	m := newMockStore()
	pk := make([][]cipher.PubKey, layers)
	for l := 0; l < layers; l++ {
		pk[l] = make([]cipher.PubKey, width)
		for w := 0; w < width; w++ {
			p, _ := cipher.GenerateKeyPair()
			pk[l][w] = p
		}
	}
	for l := 0; l < layers-1; l++ {
		for a := 0; a < width; a++ {
			for b := 0; b < width; b++ {
				m.SaveEntry(pk[l][a], pk[l+1][b], true)
			}
		}
	}
	src := pk[0][0]
	dst := pk[layers-1][0]
	g, err := NewGraph(context.Background(), m, src)
	if err != nil {
		tb.Fatal(err)
	}
	return g, src, dst
}

func BenchmarkStreamRoutesDense(b *testing.B) {
	g, src, dst := buildDenseGraph(b, 5, 6) // 5 layers × 6 wide → many 4-hop paths
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n := 0
		if err := g.StreamRoutes(context.Background(), src, dst, 0, 6, func(routing.Route) bool {
			n++
			return n < 2048
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGetRouteWeightedUncached is the raw per-request search cost — what
// the route-finder paid on EVERY repeated request before the per-graph memo.
func BenchmarkGetRouteWeightedUncached(b *testing.B) {
	g, src, dst := buildDenseGraph(b, 5, 6)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.computeRouteWeighted(context.Background(), src, dst, 0, 6, 5, true); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGetRouteWeightedCached is the fleet's actual pattern: the same
// (src,dst,params) requested repeatedly against one graph. Every request after
// the first is a memo hit — the search runs once per graph, not per request.
func BenchmarkGetRouteWeightedCached(b *testing.B) {
	g, src, dst := buildDenseGraph(b, 5, 6)
	if _, err := g.GetRouteWeighted(context.Background(), src, dst, 0, 6, 5, true); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.GetRouteWeighted(context.Background(), src, dst, 0, 6, 5, true); err != nil {
			b.Fatal(err)
		}
	}
}
