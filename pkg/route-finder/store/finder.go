// Package store pkg/route-finder/store/finder.go
package store

import (
	"context"
	"errors"
	"fmt"

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

// MaxBFSQueue caps the size of StreamRoutes' BFS queue as a memory
// safety net. Dense graphs with high maxLen can grow the queue
// exponentially; once it exceeds the cap, StreamRoutes stops adding
// new neighbors but continues draining what it has. Set to 0 to
// disable (use with caution).
var MaxBFSQueue = 200000

// StreamRoutes is the streaming form of GetRoute: it yields each valid path
// as soon as the BFS finds it, by invoking onRoute. Returning false from
// onRoute stops the search.
//
// BFS naturally explores by increasing hop length, so paths are emitted in
// shortest-first order — no post-search sort is needed for the streaming
// caller, and the caller can stop after collecting enough short paths
// without paying for deeper exploration.
//
// Memory: the working set is the BFS queue + the path being emitted; we
// never accumulate every valid path. The queue itself is capped by
// MaxBFSQueue, so even count=0 (unbounded) requests have a hard memory
// ceiling. When the cap is hit, neighbor expansion is suppressed —
// already-queued paths still drain and emit, but new branches are
// dropped, so coverage degrades while the visor stays alive.
func (g *Graph) StreamRoutes(ctx context.Context, source, destination cipher.PubKey, minLen, maxLen int, onRoute func(routing.Route) bool) error {
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
			if MaxBFSQueue > 0 && len(queue) >= MaxBFSQueue {
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
			return fmt.Errorf("BFS queue cap (%d) hit before any route found; try lower --max", MaxBFSQueue)
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
