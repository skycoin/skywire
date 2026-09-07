// Package router pkg/router/local_route_bfs.go c2-net-routing
//
// The breadth-first search behind calculateLocalRoutes' multi-hop fallback:
// given the visor's own first-hop transports and one snapshot of the
// network-wide transport graph, find the shortest path to dst whose length
// lands in [minHops, maxHops].
//
// It lives in its own file because it is the single most expensive thing the
// router does per dial — on a visor with no direct transport to the
// destination it walks the whole ~16k-edge TPD graph — and it needs to be
// benchmarkable in isolation (see local_route_bfs_test.go).
package router

import (
	"hash/fnv"
	"sort"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// localTpRef is one usable first-hop transport of the local visor, flattened
// out of the transport manager so the BFS never touches manager locks.
type localTpRef struct {
	id       uuid.UUID
	remotePK cipher.PubKey
	tpType   string
}

// localTpsByTypePref orders first-hop transports by type preference (direct
// types before DMSG). Concrete sort.Interface for the same reason as
// bfsNodesByPK: sort.SliceStable's reflect swapper is measurable here.
type localTpsByTypePref []localTpRef

func (s localTpsByTypePref) Len() int      { return len(s) }
func (s localTpsByTypePref) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s localTpsByTypePref) Less(i, j int) bool {
	return tptypes.TypePreference(tptypes.Type(s[i].tpType)) <
		tptypes.TypePreference(tptypes.Type(s[j].tpType))
}

// localTpSignature identifies the SET of first-hop transports, independent of
// the order they were collected in.
//
// This matters because it is the local half of the route memo's generation
// key, and the transports are collected by Manager.WalkTransports — which
// snapshots a Go map, so its order is randomized per call. The previous
// signature hashed the IDs in that order, so it came out different on nearly
// every call and reset the memo each time even when the transport set had not
// changed at all. Sorting the slice first does not help: it sorts by transport
// TYPE, and same-type transports keep their (random) relative order.
//
// Summation is commutative, which is exactly the property wanted; the length
// is folded in so that adding and removing transports whose id-hashes happen
// to cancel still moves the signature.
func localTpSignature(localTps []localTpRef) uint64 {
	var sum uint64
	for i := range localTps {
		h := fnv.New64a()
		id := localTps[i].id
		_, _ = h.Write(id[:])
		sum += h.Sum64()
	}
	return sum ^ (uint64(len(localTps)) * 0x9E3779B97F4A7C15)
}

// localBFSGraph is the immutable graph + per-transport metric view the BFS
// reads. All four maps come straight off one tpdSnapshot and are never
// mutated here.
type localBFSGraph struct {
	byEdge         map[cipher.PubKey][]*transport.Entry
	latencyByID    map[uuid.UUID]float64
	typeByID       map[uuid.UUID]string
	throughputByID map[uuid.UUID]float64
}

// bfsNodesByPK orders BFS frontier nodes by remote public key. A concrete
// sort.Interface, not sort.SliceStable: the reflect-based swapper that
// sort.Slice installs showed up in browser profiles of the wasm visor
// (internal/reflectlite.Swapper.func9) at a level worth avoiding in a loop
// that runs once per expanded node.
type bfsNodesByPK []bfsNode

func (s bfsNodesByPK) Len() int           { return len(s) }
func (s bfsNodesByPK) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s bfsNodesByPK) Less(i, j int) bool { return pkLess(s[i].pk, s[j].pk) }

// bfsNode is one frontier entry. The path that reaches it is NOT stored
// inline: parent indexes the arena slot of the node one hop closer to src (-1
// for a seed), and (tpID, pk) are the single hop that extends it — the hop's
// From is simply the parent's pk, or src at a seed, so it is not stored. The
// whole path is materialized only for the handful of nodes that actually hit
// dst — see materializePath.
//
// This is the allocation fix. Storing []routing.Hop per node meant one
// make+copy per graph EDGE traversed (a routing.Hop is 82 bytes, paths run to
// maxHops entries), so a single failed search over the network-wide graph
// allocated tens of megabytes and the GC — single-threaded on js/wasm — ate
// the visor's only core.
type bfsNode struct {
	pk     cipher.PubKey
	tpID   uuid.UUID
	parent int32
	self   int32
}

// bfsArena holds every node the search has created, so a node's parent chain
// stays addressable after its frontier slice is dropped. src is the origin
// every seed's chain terminates at.
type bfsArena struct {
	nodes []bfsNode
	src   cipher.PubKey
}

// add appends n to the arena and returns it with its slot index filled in.
// The index is int32 to keep bfsNode at 57 bytes; 2^31 nodes would be ~120 GB
// of arena, orders of magnitude past what maxHops over the TPD graph can
// reach, so the narrowing cannot occur in practice.
func (a *bfsArena) add(n bfsNode) bfsNode {
	n.self = int32(len(a.nodes)) //nolint:gosec // see doc comment: bounded far below 2^31
	a.nodes = append(a.nodes, n)
	return n
}

// materializePath walks n's parent chain back to the seed and returns the
// forward hop list in src→dst order.
func (a *bfsArena) materializePath(n bfsNode, depth int) []routing.Hop {
	path := make([]routing.Hop, depth)
	cur := n
	for i := depth - 1; i >= 0; i-- {
		from := a.src
		if cur.parent >= 0 {
			from = a.nodes[cur.parent].pk
		}
		path[i] = routing.Hop{TpID: cur.tpID, From: from, To: cur.pk}
		if cur.parent < 0 {
			break
		}
		cur = a.nodes[cur.parent]
	}
	return path
}

// pkInChain reports whether pk already appears anywhere on n's path (as the
// src edge or as any hop's destination) — per-path loop prevention. Walking
// the parent chain is the same work the old per-node slice scan did, minus
// the slice.
func (a *bfsArena) pkInChain(pk cipher.PubKey, n bfsNode) bool {
	cur := n
	for {
		if cur.pk == pk {
			return true
		}
		if cur.parent < 0 {
			return a.src == pk
		}
		cur = a.nodes[cur.parent]
	}
}

// expandKey identifies a (node, depth) pair for the "already expanded" set.
// One flat map instead of the old map-of-maps: same semantics, one hash
// lookup and no inner map allocated per visor.
type expandKey struct {
	pk    cipher.PubKey
	depth int
}

// localRouteBFS searches the transport graph for a path src→dst of between
// minHops and maxHops hops, seeded by the local visor's own transports.
//
// Behavior is exactly the pre-extraction inline search: levels are visited
// in increasing hop count so the first level that reaches dst wins; within a
// level every dst-hit is collected and the lowest pathLatencyScore among them
// is returned; expansion order is deterministic (children sorted by remote
// PK, seeds likewise) so identical inputs always yield an identical path.
//
// DMSG edges are excluded everywhere but the (already-handled elsewhere)
// direct case: a dmsg transport relays through a server neither endpoint can
// observe, so it cannot be accounted for as a multihop leg.
//
// ok=false means no acceptable path exists in this graph — the caller turns
// that into the "local BFS found no path" error (and caches it; a miss is as
// expensive to recompute as a hit).
func localRouteBFS(
	src, dst cipher.PubKey,
	localTps []localTpRef,
	g localBFSGraph,
	excludeIntermediates map[cipher.PubKey]struct{},
	minHops, maxHops int,
) (path []routing.Hop, level int, ok bool) {
	arena := &bfsArena{src: src}

	// Seed the BFS with the local transports (each is a 1-hop path from src
	// to a direct neighbor). Sort for determinism — same seeding order
	// across calls means same expansion order.
	//
	// DMSG seeds are dropped here on purpose. A DMSG transport relays
	// through a dmsg server (and, since server-to-server forwarding landed,
	// potentially a chain of dmsg servers) that neither end of the route can
	// observe — the intermediate server is unaccounted-for in the transport
	// entry. Using a DMSG hop anywhere in a multihop route would let data
	// transit the same dmsg server multiple times with no way to detect it.
	// Direct DMSG dials (1-hop, src→dst over a DMSG transport) are fine and
	// are handled by the caller before we get here; the BFS only produces
	// multihop paths, so we strictly require non-DMSG hops.
	seed := make([]bfsNode, 0, len(localTps))
	for _, tp := range localTps {
		if tp.tpType == "dmsg" {
			continue
		}
		if _, hit := excludeIntermediates[tp.remotePK]; hit {
			continue
		}
		seed = append(seed, arena.add(bfsNode{
			pk:     tp.remotePK,
			tpID:   tp.id,
			parent: -1,
		}))
	}
	sort.Stable(bfsNodesByPK(seed))

	// expandedAtDepth records which (pk, depth) pairs have been expanded —
	// avoids redundant work when several inbound paths converge on the same
	// node at the same depth. State is capped at O(|visors| × maxHops).
	// Without it the search could revisit identical (node, depth)
	// combinations exponentially; with it each pair expands at most once and
	// the BFS stays linear in graph size.
	expandedAtDepth := make(map[expandKey]struct{})

	// Per-TpID latency / type / throughput lookups (hydrated by the CXO
	// telemetry aggregator on TPD's side) come prebuilt from the snapshot
	// alongside byEdge. Used to rank multiple same-level dst-hits below.
	latencyFor := func(id uuid.UUID) float64 { return g.latencyByID[id] }
	typeFor := func(id uuid.UUID) string { return g.typeByID[id] }
	throughputFor := func(id uuid.UUID) float64 { return g.throughputByID[id] }

	queue := seed
	var nextQueue, children []bfsNode
	var dstHits []bfsNode
	for level = 1; level <= maxHops && len(queue) > 0; level++ {
		nextQueue = nextQueue[:0]
		// Collect ALL dst-hits at this level, then pick the lowest-latency
		// among them. Returning on the first hit left the BFS's
		// deterministic-by-PK ordering as the only tiebreaker — which
		// empirically picked geo-distant intermediates over healthier
		// same-hop-count alternatives.
		dstHits = dstHits[:0]
		for _, node := range queue {
			// Did we reach the destination at an acceptable depth?
			if node.pk == dst && level >= minHops {
				dstHits = append(dstHits, node)
				continue
			}
			// Stop expanding nodes already at maxHops — children would
			// exceed the cap.
			if level >= maxHops {
				continue
			}
			// Skip excluded intermediates (DisjointMux).
			if _, hit := excludeIntermediates[node.pk]; hit {
				continue
			}
			// Skip if this (pk, depth) was already expanded — the same
			// children would be produced. Deliberately not a global
			// visited[pk] check, which would (incorrectly) block the same
			// node at OTHER depths and could shadow longer paths when a
			// shorter one existed.
			ek := expandKey{pk: node.pk, depth: level}
			if _, done := expandedAtDepth[ek]; done {
				continue
			}
			expandedAtDepth[ek] = struct{}{}
			// Expand: every transport from this node to a new neighbor
			// becomes a candidate next node, sorted by remote PK for
			// deterministic order.
			entries := g.byEdge[node.pk]
			children = children[:0]
			for _, entry := range entries {
				if entry == nil {
					continue
				}
				if entry.Type == tptypes.DMSG {
					continue
				}
				nextPK := entry.RemoteEdge(node.pk)
				// Loop prevention: skip when the next PK is already part of
				// this path. Per-path check (not global) — the same
				// intermediate may legitimately appear in other paths at
				// other depths.
				if arena.pkInChain(nextPK, node) {
					continue
				}
				children = append(children, arena.add(bfsNode{
					pk:     nextPK,
					tpID:   entry.ID,
					parent: node.self,
				}))
			}
			sort.Stable(bfsNodesByPK(children))
			nextQueue = append(nextQueue, children...)
		}
		// If any dst-candidates were collected at this level, pick the
		// lowest-latency one and return. BFS continues to higher levels only
		// when no dst-hit happened — shorter paths still beat longer ones;
		// the score is purely a tiebreaker among same-hop-count candidates.
		if len(dstHits) > 0 {
			best := arena.materializePath(dstHits[0], level)
			bestScore := pathLatencyScore(best, latencyFor, typeFor, throughputFor)
			for _, n := range dstHits[1:] {
				p := arena.materializePath(n, level)
				if s := pathLatencyScore(p, latencyFor, typeFor, throughputFor); s < bestScore {
					best = p
					bestScore = s
				}
			}
			return best, level, true
		}
		// Swap rather than copy: nextQueue is truncated at the top of the next
		// iteration, so the two frontier buffers just alternate.
		queue, nextQueue = nextQueue, queue
	}

	return nil, 0, false
}
