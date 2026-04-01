package store

import (
	"context"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
)

type vertex struct {
	visited     bool
	edge        cipher.PubKey
	connections map[cipher.PubKey]*transport.Entry
	neighbors   map[cipher.PubKey]*vertex
}

func newVertex(edgeID cipher.PubKey, transports []*transport.Entry) *vertex {
	connections := make(map[cipher.PubKey]*transport.Entry)
	for _, tr := range transports {
		var neighbourPk cipher.PubKey
		// Check which edge is this node in the transport and add a connection to the other
		// node, it doesn't matter if that node is ourselves or a different one
		if edgeID == tr.Edges[0] {
			neighbourPk = tr.Edges[1]
		} else {
			neighbourPk = tr.Edges[0]
		}
		connections[neighbourPk] = tr
	}

	return &vertex{
		edge:        edgeID,
		connections: connections,
		visited:     false,
		neighbors:   make(map[cipher.PubKey]*vertex),
	}
}

// Graph represents a visor's connections graph (skywire network)
type Graph struct {
	store   store.Store
	visited map[cipher.PubKey]*vertex
	graph   map[cipher.PubKey]*vertex
}

// NewGraph creates a new Graph accessing given transport store, such Graph is created by exploring
// from rootPK cipher PubKey. Uses MaxGraphDepth for depth limiting.
func NewGraph(ctx context.Context, s store.Store, rootPK cipher.PubKey) (*Graph, error) {
	return NewGraphWithDepth(ctx, s, rootPK, MaxGraphDepth)
}

// NewGraphWithDepth creates a new Graph with an explicit depth limit for exploration.
func NewGraphWithDepth(ctx context.Context, s store.Store, rootPK cipher.PubKey, maxDepth int) (*Graph, error) {
	g := &Graph{
		store:   s,
		visited: make(map[cipher.PubKey]*vertex),
		graph:   make(map[cipher.PubKey]*vertex),
	}

	rootConnections, err := g.store.GetTransportsByEdge(ctx, rootPK)
	if err != nil {
		return nil, err
	}

	rootVertex := newVertex(rootPK, rootConnections)
	err = g.deepFirstSearch(ctx, rootVertex, maxDepth)
	if err != nil {
		return nil, err
	}

	// In the first iteration every vertex in the Graph has been visited
	for visited, vertex := range g.visited {
		g.graph[visited] = vertex
	}

	g.Sweep()

	return g, nil
}

// MarkAndSweep explores the Graph again from rootPK. It returns the now unreachable nodes pks by comparing
// with the previous Graph
func (g *Graph) MarkAndSweep(ctx context.Context, rootPK cipher.PubKey) ([]cipher.PubKey, error) {
	rootConnections, err := g.store.GetTransportsByEdge(ctx, rootPK)
	if err != nil {
		return nil, err
	}

	rootVertex := newVertex(rootPK, rootConnections)
	err = g.deepFirstSearch(ctx, rootVertex, MaxGraphDepth)
	if err != nil {
		return nil, err
	}

	return g.Sweep(), nil
}

// MaxGraphDepth limits how deep the graph exploration goes from the root.
// This prevents OOM on large networks. Used as default by NewGraph.
var MaxGraphDepth = 10

// deepFirstSearch explores the graph iteratively with depth limiting.
// Uses an explicit stack to avoid stack overflow on large networks.
func (g *Graph) deepFirstSearch(ctx context.Context, v *vertex, maxDepth int) error {
	type stackItem struct {
		v     *vertex
		depth int
	}

	stack := []stackItem{{v: v, depth: 0}}

	for len(stack) > 0 {
		select {
		case <-ctx.Done():
			return ErrContextClosed
		default:
		}

		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if _, ok := g.visited[item.v.edge]; ok {
			continue
		}

		item.v.visited = true
		g.visited[item.v.edge] = item.v

		if item.depth >= maxDepth {
			continue
		}

		for _, connection := range item.v.connections {
			var connectionPK cipher.PubKey
			if item.v.edge == connection.Edges[0] {
				connectionPK = connection.Edges[1]
			} else {
				connectionPK = connection.Edges[0]
			}
			if _, ok := g.visited[connectionPK]; !ok {
				connectionConnections, err := g.store.GetTransportsByEdge(ctx, connectionPK)
				if err != nil {
					return err
				}

				neighbourVertex := newVertex(connectionPK, connectionConnections)
				item.v.neighbors[connectionPK] = neighbourVertex
				stack = append(stack, stackItem{v: neighbourVertex, depth: item.depth + 1})
			} else {
				item.v.neighbors[connectionPK] = g.visited[connectionPK]
			}
		}
	}

	return nil
}

// Sweep checks which nodes cannot be reached in the Graph and prepares for next iteration
func (g *Graph) Sweep() []cipher.PubKey {
	nonReachable := make([]cipher.PubKey, 0)

	// check which nodes are not in the new Graph
	for pk := range g.graph {
		if _, ok := g.visited[pk]; !ok {
			nonReachable = append(nonReachable, pk)
		}
	}

	// copy visited into Graph, prepare for next iteration
	g.graph = make(map[cipher.PubKey]*vertex)
	for pk, vertex := range g.visited {
		g.graph[pk] = vertex
	}

	g.visited = make(map[cipher.PubKey]*vertex)

	return nonReachable
}
