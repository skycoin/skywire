// Package store pkg/deployment/rf/store/landmark_mem_test.go c2-net-routing
package store

import (
	"runtime"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// BenchmarkBuildLandmarks reports what one table build allocates and, as
// retained-B, what the tables keep on the heap afterwards.
func BenchmarkBuildLandmarks(b *testing.B) {
	g, _, _ := buildHubGraph(b, 10, 3000, 3)
	var retained uint64
	b.ReportAllocs()
	for b.Loop() {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		lt := buildLandmarks(g)
		runtime.GC()
		runtime.ReadMemStats(&after)
		retained = after.HeapAlloc - before.HeapAlloc
		runtime.KeepAlive(lt)
	}
	b.ReportMetric(float64(retained), "retained-B")
}

// composeWithReversedTable is composeLandmark as it was when the node->hub
// routes were stored, reversed once at build time, as the reference.
// reversedTable is the node->hub table buildLandmarks used to store.
func reversedTable(lt *landmarkTables) map[cipher.PubKey]map[cipher.PubKey][]routing.Route {
	toHub := make(map[cipher.PubKey]map[cipher.PubKey][]routing.Route)
	for h, fromH := range lt.fromHub {
		for node, routes := range fromH {
			if toHub[node] == nil {
				toHub[node] = make(map[cipher.PubKey][]routing.Route)
			}
			rev := make([]routing.Route, len(routes))
			for i, r := range routes {
				rev[i] = reverseRoute(r)
			}
			toHub[node][h] = rev
		}
	}
	return toHub
}

func composeWithReversedTable(g *Graph, lt *landmarkTables, toHub map[cipher.PubKey]map[cipher.PubKey][]routing.Route, src, dst cipher.PubKey, number, minLen, maxLen int) []routing.Route {
	var cands []routing.Route
	inBounds := func(r routing.Route) bool {
		return len(r.Hops) >= minLen && len(r.Hops) <= maxLen && g.routeIsLive(r)
	}
	for _, r := range lt.fromHub[src][dst] {
		if inBounds(r) {
			cands = append(cands, r)
		}
	}
	for _, r := range toHub[src][dst] {
		if inBounds(r) {
			cands = append(cands, r)
		}
	}
	for _, h := range lt.hubs {
		if h == src || h == dst {
			continue
		}
		for _, l := range toHub[src][h] {
			for _, rr := range lt.fromHub[h][dst] {
				if j, ok := joinRoutes(l, rr); ok && inBounds(j) {
					cands = append(cands, j)
				}
			}
		}
	}
	if len(cands) == 0 {
		return nil
	}
	sort.SliceStable(cands, func(i, j int) bool { return g.routeLatency(cands[i]) < g.routeLatency(cands[j]) })
	out := make([]routing.Route, 0, number)
	seen := make(map[string]struct{}, len(cands))
	for _, r := range cands {
		key := routeTpidKey(r)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, g.withCurrentLatency(r))
		if len(out) >= number {
			break
		}
	}
	return out
}

func TestLandmarkComposeMatchesReversedTable(t *testing.T) {
	g, hubs, leaves := buildHubGraph(t, 8, 120, 2)
	lt := buildLandmarks(g)
	toHub := reversedTable(lt)
	nodes := append(append([]cipher.PubKey{}, hubs...), leaves...)
	composed := 0
	for i, src := range nodes {
		for j, dst := range nodes {
			if i == j || (i*7+j)%23 != 0 {
				continue
			}
			want := composeWithReversedTable(g, lt, toHub, src, dst, 10, 0, 8)
			got := g.composeLandmark(lt, src, dst, 10, 0, 8)
			require.Equal(t, want, got)
			if len(got) > 0 {
				composed++
			}
		}
	}
	require.Greater(t, composed, 400)
}

// joinRoutes, the reference for joinReversed, concatenates l (src->hub) and r (hub->dst) into one route, rejecting
// the join if it would revisit any node (a loop across the seam). Returns ok=false
// on a loop or a broken seam (l's end != r's start).
func joinRoutes(l, r routing.Route) (routing.Route, bool) {
	if len(l.Hops) == 0 || len(r.Hops) == 0 {
		return routing.Route{}, false
	}
	if l.Hops[len(l.Hops)-1].To != r.Hops[0].From {
		return routing.Route{}, false // seam doesn't meet
	}
	seen := make(map[cipher.PubKey]struct{}, len(l.Hops)+len(r.Hops)+1)
	add := func(pk cipher.PubKey) bool {
		if _, dup := seen[pk]; dup {
			return false
		}
		seen[pk] = struct{}{}
		return true
	}
	if !add(l.Hops[0].From) {
		return routing.Route{}, false
	}
	for _, h := range l.Hops { // adds ... hub (l's last To)
		if !add(h.To) {
			return routing.Route{}, false
		}
	}
	for _, h := range r.Hops { // r.Hops[0].From == hub already in seen; add its To's
		if !add(h.To) {
			return routing.Route{}, false
		}
	}
	joined := routing.Route{Hops: make([]routing.Hop, 0, len(l.Hops)+len(r.Hops))}
	joined.Hops = append(joined.Hops, l.Hops...)
	joined.Hops = append(joined.Hops, r.Hops...)
	return joined, true
}

// BenchmarkComposeLandmark composes far leaf pairs from the new tables and,
// as Stored, from the reversed table the old build kept.
func BenchmarkComposeLandmark(b *testing.B) {
	g, _, leaves := buildHubGraph(b, 10, 3000, 3)
	lt := buildLandmarks(g)
	toHub := reversedTable(lt)
	pairs := [][2]cipher.PubKey{{leaves[0], leaves[len(leaves)-1]}, {leaves[1], leaves[1500]}, {leaves[7], leaves[2222]}}
	b.Run("Stored", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, p := range pairs {
				composeWithReversedTable(g, lt, toHub, p[0], p[1], 10, 0, 8)
			}
		}
	})
	b.Run("Reversed", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, p := range pairs {
				g.composeLandmark(lt, p[0], p[1], 10, 0, 8)
			}
		}
	})
}
