// Package store pkg/route-finder/store/finder.go
package store

import (
	"context"
	"errors"
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
func (g *Graph) GetRoute(ctx context.Context, source, destination cipher.PubKey, minLen, maxLen, number int) ([]routing.Route, error) {
	sourceVertex, ok := g.graph[source]
	if !ok {
		return nil, ErrNoRoute
	}

	destinationVertex, ok := g.graph[destination]
	if !ok {
		return nil, ErrNoRoute
	}

	paths, err := g.finder(ctx, sourceVertex, destinationVertex, minLen, maxLen)
	if err != nil {
		return nil, err
	}

	// Cap preallocated capacity to the number of paths the BFS actually
	// found. Callers may pass a large sentinel (e.g., math.MaxInt32) to
	// request "all routes"; pre-sizing to that would OOM the process.
	cap := number
	if len(paths) < cap {
		cap = len(paths)
	}
	routes := make([]routing.Route, 0, cap)
	for _, path := range paths {
		if len(routes) == number {
			break
		}

		var route routing.Route
		for i := 0; i < len(path)-1; i++ {
			from := path[i]
			to := path[i+1]
			conn, ok := from.connections[to.edge]
			if !ok {
				return nil, errors.New("connection not found between vertices")
			}
			route.Hops = append(route.Hops, routing.Hop{
				From: from.edge,
				To:   to.edge,
				TpID: conn.ID,
			})
		}
		routes = append(routes, route)
	}

	if len(routes) == 0 {
		return nil, ErrRouteNotFound
	}
	return routes, nil
}

// finder performs BFS to find all paths from source to destination with hop counts between minLen and maxLen,
// ensuring no duplicate vertices in each path.
func (g *Graph) finder(ctx context.Context, source, destination *vertex, minLen, maxLen int) ([][]*vertex, error) {
	type queueItem struct {
		current *vertex
		path    []*vertex
		hops    int
	}

	validPaths := make([][]*vertex, 0)
	queue := []queueItem{{
		current: source,
		path:    []*vertex{source},
		hops:    0,
	}}

	for len(queue) > 0 {
		select {
		case <-ctx.Done():
			return nil, ErrContextClosed
		default:
			item := queue[0]
			queue = queue[1:]

			if item.current == destination {
				if item.hops >= minLen && item.hops <= maxLen {
					validPath := make([]*vertex, len(item.path))
					copy(validPath, item.path)
					validPaths = append(validPaths, validPath)
				}
				continue
			}

			if item.hops >= maxLen {
				continue
			}

			for _, neighbor := range item.current.neighbors {
				if containsVertex(item.path, neighbor) {
					continue // Skip to avoid cycles
				}

				// DMSG transports can only appear as the last hop in a route.
				// A dmsg transport is brokered by a dmsg server intermediary that
				// is not visible to the route. Allowing two dmsg hops in one route
				// risks looping traffic through the same dmsg server, and there is
				// no way to detect or prevent that at route-build time.
				if conn, ok := item.current.connections[neighbor.edge]; ok &&
					conn.Type == tptypes.DMSG && neighbor != destination {
					continue
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
	}

	if len(validPaths) == 0 {
		return nil, ErrRouteNotFound
	}

	// Sort paths by hop count (ascending)
	sort.Slice(validPaths, func(i, j int) bool {
		return len(validPaths[i])-1 < len(validPaths[j])-1
	})

	return validPaths, nil
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
