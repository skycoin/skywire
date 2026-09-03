// Package store pkg/deployment/rf/store/finder.go c2-net-routing
package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

var (
	//ErrNoRoute no route to destination
	ErrNoRoute = errors.New("no route to destination")
	//ErrContextClosed context closed or timed out
	ErrContextClosed = errors.New("context closed or timed out")
	//ErrRouteNotFound route not found
	ErrRouteNotFound = errors.New("route not found")
)

// GetRoute returns routes from source to destination with hop counts within [minLen, maxLen],
// prioritized by shortest hop count first, with no duplicate vertices in the route.
//
// Bounded variant: stops the BFS as soon as `number` valid routes have been
// collected. Use StreamRoutes for the unbounded streaming form when memory
// matters more than the convenience of a slice.
func (g *Graph) GetRoute(ctx context.Context, source, destination cipher.PubKey, minLen, maxLen, number int) ([]routing.Route, error) {
	if number <= 0 {
		number = 1
	}
	routes := make([]routing.Route, 0, number)
	err := g.StreamRoutes(ctx, source, destination, minLen, maxLen, func(r routing.Route) bool {
		routes = append(routes, r)
		return len(routes) < number
	})
	if err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return nil, ErrRouteNotFound
	}
	return routes, nil
}

// HopLatencyPenaltyMS is the latency assumed for a transport that has no
// measured latency. It keeps latency-weighted routing usable when data is
// missing: an unmeasured edge is treated as worse than a typical measured one
// (so measured low-latency edges win), and a route of all-unmeasured edges is
// effectively ranked by hop count (number of hops × penalty) — degrading to the
// legacy behavior rather than treating missing data as free.
const HopLatencyPenaltyMS = 500.0

// routeLatency returns a route's total latency in milliseconds: the sum of each
// hop's measured transport latency from the graph, substituting
// HopLatencyPenaltyMS for any edge without a measurement.
func (g *Graph) routeLatency(r routing.Route) float64 {
	var total float64
	for _, h := range r.Hops {
		v, ok := g.graph[h.From]
		if !ok {
			total += HopLatencyPenaltyMS
			continue
		}
		conn, ok := v.connections[h.To]
		if !ok || conn.Latency <= 0 {
			total += HopLatencyPenaltyMS
			continue
		}
		total += conn.Latency
	}
	return total
}

// GetRouteWeighted returns up to `number` routes from source to destination
// within [minLen, maxLen]. When byLatency is true it returns the
// lowest-total-latency routes (a slightly longer path can win over a shorter
// high-latency one); when false it returns the shortest-hop routes — identical
// to GetRoute. Latency mode collects a bounded candidate pool from the same BFS
// and sorts it, so it stays memory-safe on dense graphs.
// routeKey identifies a memoized GetRouteWeighted result on a Graph. All
// fields are comparable, so it is a usable map/sync.Map key directly.
type routeKey struct {
	source, destination cipher.PubKey
	minLen, maxLen      int
	number              int
	byLatency           bool
}

// routeEntry is a once-computed cache slot. once guards the single BFS; the
// result is immutable afterwards and shared read-only by every caller that
// hit the same key.
type routeEntry struct {
	once   sync.Once
	routes []routing.Route
	err    error
}

// GetRouteWeighted returns up to `number` routes source→destination, memoized
// per Graph. The exhaustive BFS over the (dense) transport graph is expensive
// — seconds per request for well-connected pairs — and the fleet re-asks for
// the same pairs constantly (session churn), so without a cache the
// route-finder re-runs an identical search on every repeat. The memo collapses
// those to one BFS per unique request for the life of this graph, and its
// sync.Once also deduplicates a concurrent thundering herd of identical
// requests into a single search rather than N. Failures are not cached (the
// entry is dropped) so a transient miss can be retried immediately; only
// successful route sets persist, until the next graph rebuild swaps in a fresh
// (empty) memo. Callers must treat the returned slice as read-only.
func (g *Graph) GetRouteWeighted(ctx context.Context, source, destination cipher.PubKey, minLen, maxLen, number int, byLatency bool) ([]routing.Route, error) {
	if number <= 0 {
		number = 1
	}
	key := routeKey{source, destination, minLen, maxLen, number, byLatency}
	ei, _ := g.routeMemo.LoadOrStore(key, &routeEntry{})
	e := ei.(*routeEntry)
	e.once.Do(func() {
		e.routes, e.err = g.computeRouteWeighted(ctx, source, destination, minLen, maxLen, number, byLatency)
		if e.err != nil {
			// Don't cache failures — a later request (or a warmer graph)
			// may succeed. Concurrent waiters on this same once still see
			// this error, which is correct: they were part of this attempt.
			g.routeMemo.Delete(key)
		}
	})
	return e.routes, e.err
}

// computeRouteWeighted is the uncached search behind GetRouteWeighted.
func (g *Graph) computeRouteWeighted(ctx context.Context, source, destination cipher.PubKey, minLen, maxLen, number int, byLatency bool) ([]routing.Route, error) {
	if !byLatency {
		return g.GetRoute(ctx, source, destination, minLen, maxLen, number)
	}

	// Landmark (transit-node) routing: a budget-bounded direct BFS keeps the
	// optimal answer for every pair it can resolve cheaply, and the expensive
	// far pairs — the ones that peg the RF with a multi-second exhaustive
	// search — are answered by composing src->hub->dst instead. Returns
	// ok=false only when neither the budget BFS nor composition found anything,
	// in which case we fall through to the full exhaustive BFS below. See
	// landmark.go.
	if landmarkRoutingEnabled {
		if routes, ok := g.routesLandmarkHybrid(ctx, source, destination, minLen, maxLen, number); ok {
			return routes, nil
		}
	}

	// Pool larger than `number` so a lower-latency path that BFS emits a little
	// later (e.g. one more hop, but faster edges) can still win — bounded so
	// dense graphs don't blow up the sort.
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
	err := g.StreamRoutes(ctx, source, destination, minLen, maxLen, func(r routing.Route) bool {
		pool = append(pool, scored{route: r, latency: g.routeLatency(r)})
		return len(pool) < poolCap
	})
	if err != nil {
		return nil, err
	}
	if len(pool) == 0 {
		return nil, ErrRouteNotFound
	}

	sort.SliceStable(pool, func(i, j int) bool { return pool[i].latency < pool[j].latency })

	out := make([]routing.Route, 0, number)
	for i := 0; i < len(pool) && i < number; i++ {
		out = append(out, pool[i].route)
	}
	return out, nil
}

// DefaultMaxBFSQueue is the default per-call BFS queue cap when the
// caller of StreamRoutesWithCap passes 0 / negative. Conservative
// enough to fit comfortably on a CLI/visor without OOMing dense
// networks at high --max.
const DefaultMaxBFSQueue = 200000

// StreamRoutes is the streaming form of GetRoute: it yields each valid path
// as soon as the BFS finds it, by invoking onRoute. Returning false from
// onRoute stops the search.
//
// Equivalent to StreamRoutesWithCap with the default queue cap.
func (g *Graph) StreamRoutes(ctx context.Context, source, destination cipher.PubKey, minLen, maxLen int, onRoute func(routing.Route) bool) error {
	return g.StreamRoutesWithCap(ctx, source, destination, minLen, maxLen, DefaultMaxBFSQueue, onRoute)
}

// StreamRoutesWithCap is StreamRoutes with an explicit per-call queue
// cap. queueCap <= 0 means unbounded (use with caution; dense graphs at
// high maxLen can OOM).
//
// BFS naturally explores by increasing hop length, so paths are emitted in
// shortest-first order — no post-search sort is needed, and the caller
// can stop after collecting enough short paths without paying for deeper
// exploration.
//
// Memory: the working set is the BFS queue + the path being emitted; we
// never accumulate every valid path. The queue itself is capped by
// queueCap, so even count=0 (unbounded) requests have a hard memory
// ceiling. When the cap is hit, neighbor expansion is suppressed —
// already-queued paths still drain and emit, but new branches are
// dropped, so coverage degrades while the process stays alive.
func (g *Graph) StreamRoutesWithCap(ctx context.Context, source, destination cipher.PubKey, minLen, maxLen, queueCap int, onRoute func(routing.Route) bool) error {
	sourceVertex, ok := g.graph[source]
	if !ok {
		return ErrNoRoute
	}
	destinationVertex, ok := g.graph[destination]
	if !ok {
		return ErrNoRoute
	}

	// Parent-pointer BFS. Each item points to its PARENT instead of carrying a
	// copied path slice, so a path's ancestors are SHARED across all sibling
	// branches rather than duplicated into every descendant. That removes the
	// per-neighbor make+copy that dominated this function's cost — a CPU profile
	// of the live route-finder showed ~60% of its time in GC heap-scanning driven
	// by those path-slice allocations, plus the memmove of the copy. The full
	// path is materialized once, only when a route is actually emitted.
	queue := []*bfsItem{{current: sourceVertex, hops: 0}}
	emitted := 0
	queueCapHit := false

	for len(queue) > 0 {
		select {
		case <-ctx.Done():
			return ErrContextClosed
		default:
		}

		item := queue[0]
		queue = queue[1:]

		if item.current == destinationVertex {
			if item.hops >= minLen && item.hops <= maxLen {
				route, err := buildRoute(pathFromItem(item))
				if err != nil {
					return err
				}
				if !onRoute(route) {
					return nil
				}
				emitted++
			}
			continue
		}

		if item.hops >= maxLen {
			continue
		}

		for _, neighbor := range item.current.neighbors {
			if itemPathContains(item, neighbor) {
				continue // avoid cycles
			}

			// DMSG transports can only appear as the last hop in a route.
			// A dmsg transport is brokered by a dmsg server intermediary that
			// is not visible to the route. Allowing two dmsg hops in one route
			// risks looping traffic through the same dmsg server, and there
			// is no way to detect or prevent that at route-build time.
			if conn, ok := item.current.connections[neighbor.edge]; ok &&
				conn.Type == tptypes.DMSG && neighbor != destinationVertex {
				continue
			}

			// Memory safety: once the queue hits the cap, stop pushing
			// new items but let the existing ones drain. The flag is
			// sticky so we don't oscillate.
			if queueCap > 0 && len(queue) >= queueCap {
				queueCapHit = true
				break
			}

			queue = append(queue, &bfsItem{
				current: neighbor,
				parent:  item,
				hops:    item.hops + 1,
			})
		}
	}

	if emitted == 0 {
		if queueCapHit {
			return fmt.Errorf("BFS queue cap (%d) hit before any route found; try lower --max", queueCap)
		}
		return ErrRouteNotFound
	}
	return nil
}

// bfsItem is one frontier node of the parent-pointer route BFS: its vertex plus a
// pointer to the frontier node it was reached FROM. Ancestors are shared across
// sibling branches instead of being copied into every descendant's path slice,
// which is what removes the per-neighbor allocation that dominated the search.
type bfsItem struct {
	current *vertex
	parent  *bfsItem
	hops    int
}

// itemPathContains reports whether v already lies on the path source→it (walked
// via parent pointers). This is the cycle check; it is O(hops) and allocation-free.
func itemPathContains(it *bfsItem, v *vertex) bool {
	for p := it; p != nil; p = p.parent {
		if p.current == v {
			return true
		}
	}
	return false
}

// pathFromItem materializes the full source→it vertex path (root first) so
// buildRoute can read consecutive hops. Called ONLY when a route is emitted — not
// per neighbor expansion — so the one path allocation is paid per result, not per
// frontier node. it.hops is the index of it.current in the returned slice.
func pathFromItem(it *bfsItem) []*vertex {
	path := make([]*vertex, it.hops+1)
	for p := it; p != nil; p = p.parent {
		path[p.hops] = p.current
	}
	return path
}

// buildRoute converts a vertex path into a routing.Route by looking up
// the edge transports between consecutive hops.
func buildRoute(path []*vertex) (routing.Route, error) {
	var route routing.Route
	for i := 0; i < len(path)-1; i++ {
		from := path[i]
		to := path[i+1]
		conn, ok := from.connections[to.edge]
		if !ok {
			return routing.Route{}, errors.New("connection not found between vertices")
		}
		route.Hops = append(route.Hops, routing.Hop{
			From:    from.edge,
			To:      to.edge,
			TpID:    conn.ID,
			Latency: conn.Latency, // measured per-edge latency (ms); 0 if unmeasured
		})
	}
	return route, nil
}
