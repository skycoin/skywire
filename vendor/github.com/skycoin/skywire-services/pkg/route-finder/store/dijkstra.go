// Package store pkg/route-finder/store/dijkstra.go
package store

import (
	"container/heap"
	"context"
	"errors"
	"sort"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// package level errors
var (
	ErrNoRoute       = errors.New("no route to destination")
	ErrContextClosed = errors.New("context closed or timed out")
	ErrRouteNotFound = errors.New("route not found")
)

// dist is constant for now, can be latencies in a new implementation
const (
	infinity         = int(^uint(0) >> 1)
	distBetweenNodes = 1
)

// Shortest returns a set of max number shortest routes from source to destination which length is between minLen and
// maxLen
func (g *Graph) Shortest(ctx context.Context, source, destination cipher.PubKey, minLen, maxLen, number int) (routes []routing.Route, err error) {
	sourceVertex, ok := g.graph[source]
	if !ok {
		return nil, ErrNoRoute
	}

	destinationVertex, ok := g.graph[destination]
	if !ok {
		return nil, ErrNoRoute
	}

	previousNodes, err := g.dijkstra(ctx, sourceVertex, destinationVertex)
	if err != nil {
		return nil, err
	}
	return g.routes(ctx, previousNodes, destinationVertex, minLen, maxLen, number)
}

type previousNode struct {
	distToDestination int
	previous          *vertex
}

// Implement node version of: https://rosettacode.org/wiki/Dijkstra%27s_algorithm#Go
// dijkstra computes optimal paths from source node to every other node, but it keeps track of every other
// suboptimal route to destination and returns them
func (g *Graph) dijkstra(ctx context.Context, source, destination *vertex) ([]previousNode, error) {
	dist := make(map[*vertex]int)
	prev := make(map[*vertex]*vertex)
	destinationPrev := make([]previousNode, 0)

	sid := source
	dist[sid] = 0
	q := &priorityQueue{[]*vertex{}, make(map[*vertex]int), make(map[*vertex]int)}
	for _, v := range g.graph {
		select {
		case <-ctx.Done():
			return nil, ErrContextClosed
		default:
			if v != sid {
				dist[v] = infinity
			}
			prev[v] = nil
			q.addWithPriority(v, dist[v])
		}
	}
	for len(q.items) != 0 {
		select {
		case <-ctx.Done():
			return nil, ErrContextClosed
		default:
			u := heap.Pop(q).(*vertex)
			// Process only if there is a path from root (dist < infinity)
			if dist[u] < infinity {
				for _, v := range u.neighbors {
					if v == destination {
						alt := dist[u] + distBetweenNodes
						pn := previousNode{alt, u}
						destinationPrev = append(destinationPrev, pn)
					} else {
						alt := dist[u] + distBetweenNodes
						if alt < dist[v] {
							dist[v] = alt
							prev[v] = u
							q.update(v, alt)
						}
					}
				}
			}
		}
	}

	g.dist = dist
	g.prev = prev

	return destinationPrev, nil
}

// Route sorts by length and backtraces every route from destination to source. Only adds the paths
// with length between minLen and maxLen and returns a maximum of number routes
func (g *Graph) routes(ctx context.Context, previousNodes []previousNode, destination *vertex, minLen, maxLen, number int) ([]routing.Route, error) {
	// Sort
	sort.Slice(previousNodes, func(i, j int) bool {
		return previousNodes[i].distToDestination < previousNodes[j].distToDestination
	})

	// Backtrace
	routes := make([]routing.Route, 0)

	for _, prev := range previousNodes {
		if len(routes) == number {
			break
		}

		select {
		case <-ctx.Done():
			return nil, ErrContextClosed
		default:
			if prev.distToDestination >= minLen && prev.distToDestination <= maxLen {
				var route routing.Route
				hop := routing.Hop{
					From: prev.previous.edge,
					To:   destination.edge,
					TpID: prev.previous.connections[destination.edge].ID,
				}
				route.Hops = append(route.Hops, hop)
				prevVertex := prev.previous
				for g.prev[prevVertex] != nil {
					hop := routing.Hop{
						From: g.prev[prevVertex].edge,
						To:   prevVertex.edge,
						TpID: g.prev[prevVertex].connections[prevVertex.edge].ID,
					}
					route.Hops = append(route.Hops, hop)
					prevVertex = g.prev[prevVertex]
				}

				// because we are backtracking routes are reversed
				route = reverseRoute(route)
				routes = append(routes, route)
			}
		}
	}

	if len(routes) == 0 {
		return nil, ErrRouteNotFound
	}
	return routes, nil
}

func reverseRoute(r routing.Route) routing.Route {
	for left, right := 0, len(r.Hops)-1; left < right; left, right = left+1, right-1 {
		r.Hops[left], r.Hops[right] = r.Hops[right], r.Hops[left]
	}

	return r
}
