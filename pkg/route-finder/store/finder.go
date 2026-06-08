// Package store pkg/route-finder/store/finder.go
package store

import (
	"context"
	"errors"
	"fmt"
	"sort"

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
func (g *Graph) GetRouteWeighted(ctx context.Context, source, destination cipher.PubKey, minLen, maxLen, number int, byLatency bool) ([]routing.Route, error) {
	if number <= 0 {
		number = 1
	}
	if !byLatency {
		return g.GetRoute(ctx, source, destination, minLen, maxLen, number)
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

	type queueItem struct {
		current *vertex
		path    []*vertex
		hops    int
	}

	queue := []queueItem{{current: sourceVertex, path: []*vertex{sourceVertex}, hops: 0}}
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
				route, err := buildRoute(item.path)
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
			if containsVertex(item.path, neighbor) {
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

			newPath := make([]*vertex, len(item.path)+1)
			copy(newPath, item.path)
			newPath[len(item.path)] = neighbor

			queue = append(queue, queueItem{
				current: neighbor,
				path:    newPath,
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
			From: from.edge,
			To:   to.edge,
			TpID: conn.ID,
		})
	}
	return route, nil
}

// containsVertex checks if a vertex exists in the path.
func containsVertex(path []*vertex, v *vertex) bool {
	for _, u := range path {
		if u == v {
			return true
		}
	}
	return false
}
