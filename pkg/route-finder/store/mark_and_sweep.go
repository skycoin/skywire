package store

import (
	"context"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
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
	dist    map[*vertex]int
	prev    map[*vertex]*vertex
}

// NewGraph creates a new Graph accessing given transport store, such Graph is created by exploring
// from rootPK cipher PubKey
func NewGraph(ctx context.Context, s store.Store, rootPK cipher.PubKey) (*Graph, error) {
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
	err = g.DeepFirstSearch(ctx, rootVertex)
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
	err = g.DeepFirstSearch(ctx, rootVertex)
	if err != nil {
		return nil, err
	}

	return g.Sweep(), nil
}

// DeepFirstSearch as Mark algorithm. Recursive
func (g *Graph) DeepFirstSearch(ctx context.Context, v *vertex) error {
	select {
	case <-ctx.Done():
		return ErrContextClosed
	default:
		if _, ok := g.visited[v.edge]; !ok {
			v.visited = true
			g.visited[v.edge] = v
		}

		for _, connection := range v.connections {
			var connectionPK cipher.PubKey
			if v.edge == connection.Edges[0] {
				connectionPKEdge := connection.Edges[1]
				connectionPK = connectionPKEdge
			} else {
				connectionPKEdge := connection.Edges[0]
				connectionPK = connectionPKEdge
			}
			if _, ok := g.visited[connectionPK]; !ok {
				connectionConnections, err := g.store.GetTransportsByEdge(ctx, connectionPK)
				if err != nil {
					return err
				}

				neighbourVertex := newVertex(connectionPK, connectionConnections)
				v.neighbors[connectionPK] = neighbourVertex
				if err = g.DeepFirstSearch(ctx, neighbourVertex); err != nil {
					return err
				}
			} else {
				v.neighbors[connectionPK] = g.visited[connectionPK]
			}
		}

		return nil
	}
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
