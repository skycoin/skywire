// Package store pkg/deployment/rf/store/landmark.go c2-net-routing
//
// Landmark (transit-node) routing for the route-finder. The base route-finder
// answers each (src,dst) with a bounded but EXHAUSTIVE BFS over the whole dense
// transport graph; on the live network a single far/hard pair has been measured
// at ~8.4s of CPU and pegs the RF host. #4380 (cached graph) and #4385
// (per-graph result memo) removed the per-request graph BUILD and the REPEAT
// BFS, but the first search of any pair is still exhaustive.
//
// This exploits the mesh's structure: a handful of very-high-degree hubs
// (magnetosphere/prod02/the production exit carry ~1200-1300 transports each vs
// ~70-120 for a normal node) that most routes already transit, plus
// deterministic tpids and durable topology (see rf-landmark-routing-rfc.md).
// We precompute routes between every node and those hubs ONCE per graph, then
// answer a far (src,dst) by COMPOSING src->hub + hub->dst — ~H*K*K cheap
// concatenations instead of a graph-wide search.
//
// It is a strict addition: a pair the (budget-bounded) direct BFS resolves
// cheaply keeps its optimal exhaustive answer; only the pairs that blow past the
// budget — the multi-second cases that peg the RF — fall to composition, and a
// composition miss still falls through to the full BFS. Coexists with #4380 and
// #4385 (the memo caches composed results exactly as it caches BFS results).
package store

import (
	"bytes"
	"context"
	"os"
	"sort"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	// defaultLandmarkHubs is how many top-degree nodes become landmarks.
	defaultLandmarkHubs = 8
	// defaultLandmarkK is how many routes are stored per (node,hub) direction.
	defaultLandmarkK = 5
	// defaultLandmarkMaxLen bounds each precomputed sub-route's hop length.
	defaultLandmarkMaxLen = 6
	// landmarkBFSBudget bounds the direct BFS attempted BEFORE composing. A pair
	// the BFS resolves within this many queued frontier nodes keeps its optimal
	// exhaustive answer; a pair that exceeds it (the pathological multi-second
	// searches) falls to composition instead of pegging a core.
	landmarkBFSBudget = 40000
	// landmarkDirectDepth caps the direct BFS's hop depth on the first pass.
	// Pairs with enough routes within this many hops keep their optimal
	// exhaustive answer cheaply; a farther pair exhausts this shallow depth
	// FAST (bounded frontier) and falls to composition — that fast-fail is what
	// turns a multi-second exhaustive search into ~microseconds. Most real
	// routes are ≤3 hops (with hubs, leaf→hub→leaf is 2), so this serves the
	// common case directly and composes only the genuinely-far tail.
	landmarkDirectDepth = 3
)

// landmarkRoutingEnabled gates the feature. On unless RF_LANDMARK_ROUTING=0, so
// it can be reverted on the deployment host without a rebuild.
var landmarkRoutingEnabled = os.Getenv("RF_LANDMARK_ROUTING") != "0"

// landmarkTables holds, for one immutable Graph, the precomputed routes between
// every node and the selected hubs. Routes are full routing.Route values (not
// just tpid sequences) so composition and latency-ranking reuse the existing
// buildRoute/routeLatency machinery unchanged; a follow-up can compact these to
// tpid-only if the ~10MB footprint ever matters.
type landmarkTables struct {
	hubs    []cipher.PubKey
	toHub   map[cipher.PubKey]map[cipher.PubKey][]routing.Route // node -> hub  -> routes
	fromHub map[cipher.PubKey]map[cipher.PubKey][]routing.Route // hub  -> node -> routes
}

// cipherLess is a deterministic total order on pubkeys (byte order), used for
// stable hub-selection tiebreaks so the same graph always picks the same hubs.
func cipherLess(a, b cipher.PubKey) bool { return bytes.Compare(a[:], b[:]) < 0 }

// selectHubs returns the n highest-degree nodes (degree = number of distinct
// neighbors = len(vertex.connections)), deterministic on ties. The mega-hubs
// and any future high-degree node are captured automatically; the set re-derives
// each time the graph is (re)built, so it self-heals as topology changes.
func selectHubs(g *Graph, n int) []cipher.PubKey {
	type deg struct {
		pk  cipher.PubKey
		deg int
	}
	ds := make([]deg, 0, len(g.graph))
	for pk, v := range g.graph {
		ds = append(ds, deg{pk, len(v.connections)})
	}
	sort.Slice(ds, func(i, j int) bool {
		if ds[i].deg != ds[j].deg {
			return ds[i].deg > ds[j].deg
		}
		return cipherLess(ds[i].pk, ds[j].pk)
	})
	if n > len(ds) {
		n = len(ds)
	}
	out := make([]cipher.PubKey, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, ds[i].pk)
	}
	return out
}

// routesFromHub does ONE BFS from hub and records up to K shortest routes to
// EVERY reachable node (the frontier reaches nodes shortest-first, exactly as
// StreamRoutesWithCap emits). Reuses the parent-index bfsItem frontier from
// #4378. dmsg transports are excluded entirely: dmsg is only valid as a route's
// final hop, but a landmark sub-route's endpoint becomes an interior junction
// after composition — so a dmsg hop there would sit mid-route. Direct
// dmsg-terminated routes are still served by the full-BFS fallback.
func (g *Graph) routesFromHub(hub cipher.PubKey, k, maxLen, queueCap int) map[cipher.PubKey][]routing.Route {
	out := make(map[cipher.PubKey][]routing.Route)
	hubV, ok := g.graph[hub]
	if !ok {
		return out
	}
	queue := []*bfsItem{{current: hubV, hops: 0}}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		if item.hops >= 1 && item.hops <= maxLen {
			pk := item.current.edge
			if len(out[pk]) < k {
				if r, err := buildRoute(pathFromItem(item)); err == nil {
					out[pk] = append(out[pk], r)
				}
			}
		}
		if item.hops >= maxLen {
			continue
		}
		for _, neighbor := range item.current.neighbors {
			if itemPathContains(item, neighbor) {
				continue
			}
			if conn, ok := item.current.connections[neighbor.edge]; ok && conn.Type == tptypes.DMSG {
				continue
			}
			if queueCap > 0 && len(queue) >= queueCap {
				break
			}
			queue = append(queue, &bfsItem{current: neighbor, parent: item, hops: item.hops + 1})
		}
	}
	return out
}

// reverseRoute flips a route's direction. Transports are bidirectional and their
// tpid is edge-symmetric (MakeTransportID sorts the edges), so the reverse of a
// valid hub->node route is a valid node->hub route with the same tpids and
// per-edge latencies.
func reverseRoute(r routing.Route) routing.Route {
	n := len(r.Hops)
	out := routing.Route{Hops: make([]routing.Hop, n)}
	for i, h := range r.Hops {
		out.Hops[n-1-i] = routing.Hop{From: h.To, To: h.From, TpID: h.TpID, Latency: h.Latency}
	}
	return out
}

// buildLandmarks materializes the node<->hub tables from g via ~2H hub-rooted
// BFS (one per hub; the reverse fills node->hub for free). O(H * BFS), not
// O(pairs * BFS).
func buildLandmarks(g *Graph) *landmarkTables {
	hubs := selectHubs(g, defaultLandmarkHubs)
	lt := &landmarkTables{
		hubs:    hubs,
		toHub:   make(map[cipher.PubKey]map[cipher.PubKey][]routing.Route),
		fromHub: make(map[cipher.PubKey]map[cipher.PubKey][]routing.Route),
	}
	for _, h := range hubs {
		fromH := g.routesFromHub(h, defaultLandmarkK, defaultLandmarkMaxLen, landmarkBFSBudget)
		lt.fromHub[h] = fromH
		for node, routes := range fromH {
			if lt.toHub[node] == nil {
				lt.toHub[node] = make(map[cipher.PubKey][]routing.Route)
			}
			rev := make([]routing.Route, len(routes))
			for i, r := range routes {
				rev[i] = reverseRoute(r)
			}
			lt.toHub[node][h] = rev
		}
	}
	return lt
}

// ensureLandmarks builds the tables once per Graph (graphs are immutable and
// swapped each 15s GraphCache refresh, so the tables live exactly as long as the
// graph — same lifetime discipline as #4385's routeMemo).
func (g *Graph) ensureLandmarks() *landmarkTables {
	g.landmarkOnce.Do(func() { g.landmarks = buildLandmarks(g) })
	return g.landmarks
}

// joinRoutes concatenates l (src->hub) and r (hub->dst) into one route, rejecting
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

// routeTpidKey is a dedupe key: the ordered tpids of a route.
func routeTpidKey(r routing.Route) string {
	var b bytes.Buffer
	for i := range r.Hops {
		b.Write(r.Hops[i].TpID[:])
	}
	return b.String()
}

// composeLandmark answers src->dst by joining src->hub with hub->dst over every
// hub, loop-checking each seam, filtering to [minLen,maxLen] hops, then ranking
// by the SAME latency weighting the base finder uses and returning up to `number`
// distinct routes. Composing via DIFFERENT hubs yields structurally disjoint
// legs (they share only src and dst) — what the mux wants.
func (g *Graph) composeLandmark(lt *landmarkTables, src, dst cipher.PubKey, number, minLen, maxLen int) []routing.Route {
	if lt == nil {
		return nil
	}
	var cands []routing.Route
	inBounds := func(r routing.Route) bool {
		return len(r.Hops) >= minLen && len(r.Hops) <= maxLen
	}
	// Degenerate: src or dst IS a hub — the tables already hold the direct
	// node<->hub routes, no composition needed.
	if fh, ok := lt.fromHub[src]; ok {
		for _, r := range fh[dst] {
			if inBounds(r) {
				cands = append(cands, r)
			}
		}
	}
	if th, ok := lt.toHub[src]; ok {
		for _, r := range th[dst] { // dst is a hub
			if inBounds(r) {
				cands = append(cands, r)
			}
		}
	}
	// General case: src->hub + hub->dst.
	srcTo := lt.toHub[src]
	for _, h := range lt.hubs {
		if h == src || h == dst {
			continue
		}
		left := srcTo[h]
		right := lt.fromHub[h][dst]
		for _, l := range left {
			for _, rr := range right {
				if j, ok := joinRoutes(l, rr); ok && inBounds(j) {
					cands = append(cands, j)
				}
			}
		}
	}
	if len(cands) == 0 {
		return nil
	}
	sort.SliceStable(cands, func(i, j int) bool {
		return g.routeLatency(cands[i]) < g.routeLatency(cands[j])
	})
	out := make([]routing.Route, 0, number)
	seen := make(map[string]struct{}, len(cands))
	for _, r := range cands {
		key := routeTpidKey(r)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, r)
		if len(out) >= number {
			break
		}
	}
	return out
}

// routeIsLive reports whether every hop of r still exists in the current graph
// (its tpid present on the from-vertex). A composed route built from an earlier
// snapshot can reference a transport that a hub restart briefly dropped; the
// tpid re-registers deterministically, but until it does we must not hand out a
// route over a dead edge.
func (g *Graph) routeIsLive(r routing.Route) bool {
	for i := range r.Hops {
		v, ok := g.graph[r.Hops[i].From]
		if !ok {
			return false
		}
		conn, ok := v.connections[r.Hops[i].To]
		if !ok || conn.ID != r.Hops[i].TpID {
			return false
		}
	}
	return true
}

// pooledBFS runs the base latency-ranked BFS with an explicit queue cap and
// returns the routes ranked lowest-latency-first. This is the exact pool logic
// from computeRouteWeighted, extracted so the landmark hybrid can run it with a
// small budget (fast-fail on far pairs) and the fallback can run it with the
// full cap.
func (g *Graph) pooledBFS(ctx context.Context, src, dst cipher.PubKey, minLen, maxLen, number, queueCap int) []routing.Route {
	poolCap := number * 32
	if poolCap < 128 {
		poolCap = 128
	}
	if poolCap > 2048 {
		poolCap = 2048
	}
	type scored struct {
		route   routing.Route
		latency float64
	}
	pool := make([]scored, 0, poolCap)
	_ = g.StreamRoutesWithCap(ctx, src, dst, minLen, maxLen, queueCap, func(r routing.Route) bool {
		pool = append(pool, scored{route: r, latency: g.routeLatency(r)})
		return len(pool) < poolCap
	})
	sort.SliceStable(pool, func(i, j int) bool { return pool[i].latency < pool[j].latency })
	out := make([]routing.Route, len(pool))
	for i := range pool {
		out[i] = pool[i].route
	}
	return out
}

// routesLandmarkHybrid is the landmark serve path. It returns (routes, true) when
// it produced an answer, or (nil, false) to tell the caller to fall through to
// the full exhaustive BFS (the ultimate correctness fallback).
//
//  1. Budget-bounded direct BFS first: a pair resolvable within landmarkBFSBudget
//     keeps its optimal exhaustive answer — unchanged behavior for every pair the
//     BFS can serve cheaply.
//  2. Otherwise (the expensive/far pairs that peg the RF) compose src->hub->dst,
//     drop any composition over a currently-dead tpid, and merge with whatever the
//     budget BFS did find.
//  3. If neither found anything, return false so the caller runs the full BFS.
func (g *Graph) routesLandmarkHybrid(ctx context.Context, src, dst cipher.PubKey, minLen, maxLen, number int) ([]routing.Route, bool) {
	directDepth := maxLen
	if directDepth > landmarkDirectDepth {
		directDepth = landmarkDirectDepth
	}
	pool := g.pooledBFS(ctx, src, dst, minLen, directDepth, number, landmarkBFSBudget)
	if len(pool) >= number {
		return pool[:number], true
	}
	lt := g.ensureLandmarks()
	composed := g.composeLandmark(lt, src, dst, number, minLen, maxLen)
	if len(composed) > 0 {
		live := composed[:0]
		for _, r := range composed {
			if g.routeIsLive(r) {
				live = append(live, r)
			}
		}
		composed = live
	}
	if len(pool) == 0 && len(composed) == 0 {
		return nil, false // let the exhaustive BFS fallback try
	}
	// Merge budget-BFS routes and composed routes, dedupe, rank by latency.
	merged := make([]routing.Route, 0, len(pool)+len(composed))
	seen := make(map[string]struct{}, len(pool)+len(composed))
	for _, r := range append(pool, composed...) {
		key := routeTpidKey(r)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, r)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return g.routeLatency(merged[i]) < g.routeLatency(merged[j])
	})
	if len(merged) > number {
		merged = merged[:number]
	}
	return merged, true
}
