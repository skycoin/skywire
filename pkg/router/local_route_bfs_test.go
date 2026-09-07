// pkg/router/local_route_bfs_test.go — equivalence + cost of the local-route
// BFS rewrite.
//
// legacyLocalRouteBFS below is a verbatim copy of the search as it stood before
// the parent-arena rewrite (per-node []routing.Hop, map-of-maps expansion set,
// sort.SliceStable). It exists for two reasons: TestLocalRouteBFSMatchesLegacy
// proves the new search returns the SAME path on randomized graphs, and
// BenchmarkLocalRouteBFS measures the two side by side on identical input so
// the before/after numbers come from one run.

// Package router pkg/router/local_route_bfs_test.go c2-net-routing
package router

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// legacyLocalRouteBFS is the pre-rewrite search, kept for A/B comparison.
func legacyLocalRouteBFS(
	src, dst cipher.PubKey,
	localTps []localTpRef,
	g localBFSGraph,
	excludeIntermediates map[cipher.PubKey]struct{},
	minHops, maxHops int,
) (path []routing.Hop, level int, ok bool) {
	type legacyNode struct {
		pk   cipher.PubKey
		path []routing.Hop
	}

	seed := make([]legacyNode, 0, len(localTps))
	for _, tp := range localTps {
		if tp.tpType == "dmsg" {
			continue
		}
		if _, hit := excludeIntermediates[tp.remotePK]; hit {
			continue
		}
		seed = append(seed, legacyNode{
			pk:   tp.remotePK,
			path: []routing.Hop{{TpID: tp.id, From: src, To: tp.remotePK}},
		})
	}
	sort.SliceStable(seed, func(i, j int) bool { return pkLess(seed[i].pk, seed[j].pk) })

	pkInPath := func(pk cipher.PubKey, path []routing.Hop) bool {
		if len(path) > 0 && path[0].From == pk {
			return true
		}
		for _, h := range path {
			if h.To == pk {
				return true
			}
		}
		return false
	}

	expandedAtDepth := make(map[cipher.PubKey]map[int]bool)
	latencyFor := func(id uuid.UUID) float64 { return g.latencyByID[id] }
	typeFor := func(id uuid.UUID) string { return g.typeByID[id] }
	throughputFor := func(id uuid.UUID) float64 { return g.throughputByID[id] }

	queue := seed
	for level = 1; level <= maxHops && len(queue) > 0; level++ {
		nextQueue := make([]legacyNode, 0)
		var dstCandidates [][]routing.Hop
		for _, node := range queue {
			if node.pk == dst && level >= minHops {
				dstCandidates = append(dstCandidates, node.path)
				continue
			}
			if level >= maxHops {
				continue
			}
			if _, hit := excludeIntermediates[node.pk]; hit {
				continue
			}
			if expandedAtDepth[node.pk][level] {
				continue
			}
			if expandedAtDepth[node.pk] == nil {
				expandedAtDepth[node.pk] = make(map[int]bool)
			}
			expandedAtDepth[node.pk][level] = true
			entries := g.byEdge[node.pk]
			children := make([]legacyNode, 0, len(entries))
			for _, entry := range entries {
				if entry == nil {
					continue
				}
				if entry.Type == tptypes.DMSG {
					continue
				}
				nextPK := entry.RemoteEdge(node.pk)
				if pkInPath(nextPK, node.path) {
					continue
				}
				newPath := make([]routing.Hop, len(node.path), len(node.path)+1)
				copy(newPath, node.path)
				newPath = append(newPath, routing.Hop{TpID: entry.ID, From: node.pk, To: nextPK})
				children = append(children, legacyNode{pk: nextPK, path: newPath})
			}
			sort.SliceStable(children, func(i, j int) bool { return pkLess(children[i].pk, children[j].pk) })
			nextQueue = append(nextQueue, children...)
		}
		if len(dstCandidates) > 0 {
			best := dstCandidates[0]
			bestScore := pathLatencyScore(best, latencyFor, typeFor, throughputFor)
			for _, p := range dstCandidates[1:] {
				if s := pathLatencyScore(p, latencyFor, typeFor, throughputFor); s < bestScore {
					best = p
					bestScore = s
				}
			}
			return best, level, true
		}
		queue = nextQueue
	}
	return nil, 0, false
}

// bfsFixture is one synthetic transport graph plus the local visor's first-hop
// set — shaped like the live TPD dataset the wasm visor works over.
type bfsFixture struct {
	src, dst cipher.PubKey
	localTps []localTpRef
	graph    localBFSGraph
}

// newBFSFixture builds a random connected graph of `visors` nodes with roughly
// `degree` transports each. When reachable is false, dst is an isolated visor
// with no edges at all — the expensive "search finds nothing" case, which is
// what a NAT'd visor dialing an unreachable destination actually runs.
func newBFSFixture(seed int64, visors, degree int, reachable bool) bfsFixture {
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // deterministic fixture

	pks := make([]cipher.PubKey, visors)
	for i := range pks {
		var pk cipher.PubKey
		_, _ = rng.Read(pk[:]) //nolint:staticcheck // deterministic fixture source
		pks[i] = pk
	}
	src := pks[0]
	dst := pks[visors-1]

	var entries []*transport.Entry
	// dst is the last visor; when unreachable we simply never wire an edge to
	// it (its index is skipped as an endpoint).
	limit := visors
	if !reachable {
		limit = visors - 1
	}
	for i := 0; i < limit; i++ {
		for d := 0; d < degree; d++ {
			j := rng.Intn(limit)
			if j == i {
				continue
			}
			typ := tptypes.STCPR
			if rng.Intn(4) == 0 {
				typ = tptypes.SUDPH
			}
			entries = append(entries, &transport.Entry{
				ID:            transport.MakeTransportID(pks[i], pks[j], typ),
				Type:          typ,
				Edges:         [2]cipher.PubKey{pks[i], pks[j]},
				Latency:       float64(rng.Intn(200) + 1),
				ThroughputBps: float64(rng.Intn(1_000_000)),
			})
		}
	}

	byEdge, lat, typ, thr := deriveTransportLookups(entries)

	// The local visor's own first hops: a handful of direct neighbors.
	var localTps []localTpRef
	for _, e := range byEdge[src] {
		localTps = append(localTps, localTpRef{
			id:       e.ID,
			remotePK: e.RemoteEdge(src),
			tpType:   string(e.Type),
		})
		if len(localTps) == 4 {
			break
		}
	}

	return bfsFixture{
		src:      src,
		dst:      dst,
		localTps: localTps,
		graph:    localBFSGraph{byEdge: byEdge, latencyByID: lat, typeByID: typ, throughputByID: thr},
	}
}

// TestLocalRouteBFSMatchesLegacy: the parent-arena search returns byte-identical
// paths to the pre-rewrite one across randomized graphs, reachable and not, at
// every hop bound. This is the correctness argument for the rewrite — the route
// selected cannot change.
func TestLocalRouteBFSMatchesLegacy(t *testing.T) {
	for _, reachable := range []bool{true, false} {
		for seed := int64(1); seed <= 12; seed++ {
			for _, maxHops := range []int{2, 3, 5, 7} {
				f := newBFSFixture(seed, 60, 3, reachable)
				if len(f.localTps) == 0 {
					continue
				}
				name := fmt.Sprintf("reach=%v/seed=%d/max=%d", reachable, seed, maxHops)
				t.Run(name, func(t *testing.T) {
					wantPath, wantLevel, wantOK := legacyLocalRouteBFS(
						f.src, f.dst, f.localTps, f.graph, nil, 2, maxHops)
					gotPath, gotLevel, gotOK := localRouteBFS(
						f.src, f.dst, f.localTps, f.graph, nil, 2, maxHops)
					assert.Equal(t, wantOK, gotOK, "found-ness must match")
					assert.Equal(t, wantLevel, gotLevel, "hop count must match")
					assert.Equal(t, wantPath, gotPath, "selected path must match exactly")
				})
			}
		}
	}
}

// TestLocalRouteBFSHonorsExclusions: intermediate exclusions (DisjointMux) keep
// working after the rewrite, and both implementations agree.
func TestLocalRouteBFSHonorsExclusions(t *testing.T) {
	f := newBFSFixture(7, 60, 3, true)
	require.NotEmpty(t, f.localTps)

	path, _, ok := localRouteBFS(f.src, f.dst, f.localTps, f.graph, nil, 2, 5)
	require.True(t, ok, "baseline route must exist")
	require.NotEmpty(t, path)

	// Exclude the first intermediate the baseline chose; the new path must not
	// route through it, and must equal what the legacy search picks.
	excl := map[cipher.PubKey]struct{}{path[0].To: {}}
	gotPath, gotLevel, gotOK := localRouteBFS(f.src, f.dst, f.localTps, f.graph, excl, 2, 5)
	wantPath, wantLevel, wantOK := legacyLocalRouteBFS(f.src, f.dst, f.localTps, f.graph, excl, 2, 5)
	assert.Equal(t, wantOK, gotOK)
	assert.Equal(t, wantLevel, gotLevel)
	assert.Equal(t, wantPath, gotPath)
	for _, h := range gotPath {
		if h.To != f.dst {
			assert.NotEqual(t, path[0].To, h.To, "excluded intermediate must not appear")
		}
	}
}

// TestLocalRouteBFSNoLoops: no PK appears twice in a returned path.
func TestLocalRouteBFSNoLoops(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		f := newBFSFixture(seed, 80, 4, true)
		if len(f.localTps) == 0 {
			continue
		}
		path, _, ok := localRouteBFS(f.src, f.dst, f.localTps, f.graph, nil, 2, 6)
		if !ok {
			continue
		}
		seen := map[cipher.PubKey]int{f.src: 1}
		for _, h := range path {
			seen[h.To]++
			assert.Equal(t, 1, seen[h.To], "pk %s repeats in path", h.To)
		}
	}
}

// BenchmarkLocalRouteBFS measures the search on a graph sized like the live TPD
// dataset. The "unreachable" variants are the ones that matter: with no path to
// dst the search exhausts the graph to maxHops, which is exactly the case a
// NAT'd visor hits on every retry.
func BenchmarkLocalRouteBFS(b *testing.B) {
	cases := []struct {
		name      string
		visors    int
		degree    int
		maxHops   int
		reachable bool
	}{
		{"reachable/1k-visors/max5", 1000, 4, 5, true},
		{"unreachable/1k-visors/max5", 1000, 4, 5, false},
		{"unreachable/2k-visors/max7", 2000, 4, 7, false},
	}
	for _, c := range cases {
		f := newBFSFixture(42, c.visors, c.degree, c.reachable)
		if len(f.localTps) == 0 {
			b.Fatalf("fixture %s produced no local transports", c.name)
		}
		b.Run("new/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				localRouteBFS(f.src, f.dst, f.localTps, f.graph, nil, 2, c.maxHops)
			}
		})
		b.Run("legacy/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				legacyLocalRouteBFS(f.src, f.dst, f.localTps, f.graph, nil, 2, c.maxHops)
			}
		})
	}
}

// BenchmarkLocalRouteMemo contrasts a memo hit with recomputing the search. This
// is the other half of the fix: before, the memo was gated on a non-zero CXO
// snapshot timestamp, so on the TTL path (and on any visor whose CXO feed never
// primes) every call paid the full search below.
func BenchmarkLocalRouteMemo(b *testing.B) {
	f := newBFSFixture(42, 1000, 4, true)
	path, _, ok := localRouteBFS(f.src, f.dst, f.localTps, f.graph, nil, 2, 5)
	require.True(b, ok)
	rev := reverseHops(path)

	memo := newLocalRouteMemo()
	key := localRouteKey{src: f.src, dst: f.dst, min: 2, max: 5}
	memo.put(1, 99, key, path, rev)

	b.Run("memo-hit", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, _, _, hit := memo.get(1, 99, key); !hit {
				b.Fatal("expected memo hit")
			}
		}
	})
	b.Run("recompute", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			localRouteBFS(f.src, f.dst, f.localTps, f.graph, nil, 2, 5)
		}
	})
}

// TestLocalRouteMemoCachesMisses: a searched-and-empty result is cached, and a
// generation change drops it.
func TestLocalRouteMemoCachesMisses(t *testing.T) {
	memo := newLocalRouteMemo()
	key := localRouteKey{src: cipher.PubKey{0x1}, dst: cipher.PubKey{0x2}, min: 2, max: 5}

	_, _, _, ok := memo.get(1, 1, key)
	assert.False(t, ok, "empty memo must miss")

	memo.putMiss(1, 1, key)
	fwd, rev, found, ok := memo.get(1, 1, key)
	assert.True(t, ok, "a recorded miss is a cache hit")
	assert.False(t, found, "and reports no path")
	assert.Nil(t, fwd)
	assert.Nil(t, rev)

	// New snapshot generation invalidates.
	_, _, _, ok = memo.get(2, 1, key)
	assert.False(t, ok, "generation change must invalidate")
	// So does a change in the local first-hop set.
	memo.putMiss(1, 1, key)
	_, _, _, ok = memo.get(1, 2, key)
	assert.False(t, ok, "local-transport signature change must invalidate")
}

// TestLocalRouteMemoWorksWithoutCXOVersion is the regression for the CPU peg:
// the memo keys on the snapshot cache's generation counter, which is set on the
// TTL path too. Before, it keyed on tpdSnapshot.version — the zero time
// whenever the discovery client reports no CXO sync timestamp — and
// calculateLocalRoutes disabled the memo outright for a zero version.
func TestLocalRouteMemoWorksWithoutCXOVersion(t *testing.T) {
	c := newTPDSnapshotCache()
	fetch := func(_ context.Context) ([]*transport.Entry, error) {
		return []*transport.Entry{{
			ID:    uuid.New(),
			Type:  tptypes.STCPR,
			Edges: [2]cipher.PubKey{{0xA}, {0xB}},
		}}, nil
	}

	// No version probe at all — the plain-HTTP / unprimed-CXO path.
	snap, err := c.snapshot(context.Background(), fetch, nil)
	require.NoError(t, err)
	assert.True(t, snap.version.IsZero(), "TTL path carries no CXO timestamp")
	assert.NotZero(t, snap.gen, "but it does carry a usable generation")

	// Same snapshot served again keeps the same generation, so the memo holds.
	again, err := c.snapshot(context.Background(), fetch, nil)
	require.NoError(t, err)
	assert.Equal(t, snap.gen, again.gen)
}

// TestLocalTpSignatureOrderIndependent is the second half of the memo
// regression: WalkTransports hands back the transport set in randomized map
// order, so a signature that depends on that order changes on nearly every
// call and resets the memo even when nothing changed.
func TestLocalTpSignatureOrderIndependent(t *testing.T) {
	tps := []localTpRef{
		{id: uuid.MustParse("11111111-1111-1111-1111-111111111111")},
		{id: uuid.MustParse("22222222-2222-2222-2222-222222222222")},
		{id: uuid.MustParse("33333333-3333-3333-3333-333333333333")},
		{id: uuid.MustParse("44444444-4444-4444-4444-444444444444")},
	}
	want := localTpSignature(tps)

	rng := rand.New(rand.NewSource(5)) //nolint:gosec // deterministic shuffle
	for i := 0; i < 50; i++ {
		shuffled := append([]localTpRef(nil), tps...)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		assert.Equal(t, want, localTpSignature(shuffled), "signature must not depend on collection order")
	}

	// But it must still move when the SET changes.
	assert.NotEqual(t, want, localTpSignature(tps[:3]), "dropping a transport must change the signature")
	assert.NotEqual(t, want, localTpSignature(append(append([]localTpRef(nil), tps...),
		localTpRef{id: uuid.MustParse("55555555-5555-5555-5555-555555555555")})),
		"adding a transport must change the signature")
}
