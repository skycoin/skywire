// Package store pkg/deployment/rf/store/graph_full.go c2-net-routing
package store

import (
	"context"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/transport"
)

// NewFullGraph builds the COMPLETE network graph from a single GetAllTransports
// call, instead of walking the store per-vertex from a root (NewGraphWithDepth,
// which issues a GetTransportsByEdge for every node it discovers). One bulk read
// + O(edges) in-memory linking replaces thousands of round trips, so the result
// is cheap enough to build once and share across every route request (see
// GraphCache). The graph holds all reachable nodes (no per-source depth limit);
// route length is still bounded per request by the BFS maxLen.
//
// The vertex/connection/neighbor shape is identical to the root-explored graph —
// newVertex applies the same setup-label exclusion and per-neighbor dedup — so
// StreamRoutes/GetRoute return the same routes they would on a NewGraph rooted at
// the same source.
// latencyBulkStore is the optional capability of a store that can return all
// transports with durable per-edge latency overlaid in a single read. The redis
// TPD store implements it; the in-memory store / mocks do not, so NewFullGraph
// falls back to the latency-free GetAllTransports there.
type latencyBulkStore interface {
	GetAllTransportsWithLatency(ctx context.Context, selfTransports bool) ([]*transport.Entry, error)
}

func NewFullGraph(ctx context.Context, s store.Store) (*Graph, error) {
	entries, err := allTransportsForGraph(ctx, s)
	if err != nil {
		return nil, err
	}
	return graphFromEntries(s, entries), nil
}

// allTransportsForGraph does the single bulk read the full graph is built from,
// preferring the latency-overlaid variant when the store supports it.
func allTransportsForGraph(ctx context.Context, s store.Store) ([]*transport.Entry, error) {
	if lb, ok := s.(latencyBulkStore); ok {
		return lb.GetAllTransportsWithLatency(ctx, false)
	}
	return s.GetAllTransports(ctx, false)
}

// graphFromEntries builds the complete graph from a transport-entry slice — the
// in-memory linking step shared by NewFullGraph and the GraphCache (which reads
// the entries once, signs them, and only rebuilds when the set changed).
func graphFromEntries(s store.Store, entries []*transport.Entry) *Graph {
	// Group every transport under BOTH of its edge PKs — each node's connection
	// list is exactly the transports it is an edge of.
	byPK := make(map[cipher.PubKey][]*transport.Entry)
	for _, e := range entries {
		if e == nil {
			continue
		}
		byPK[e.Edges[0]] = append(byPK[e.Edges[0]], e)
		byPK[e.Edges[1]] = append(byPK[e.Edges[1]], e)
	}

	g := &Graph{
		store:   s,
		visited: make(map[cipher.PubKey]*vertex),
		graph:   make(map[cipher.PubKey]*vertex, len(byPK)),
	}

	// Create one canonical vertex per PK (newVertex builds its connections map,
	// keyed by the OTHER edge, with setup transports excluded and duplicates
	// collapsed to the preferred transport).
	for pk, tps := range byPK {
		g.graph[pk] = newVertex(pk, tps)
	}

	// Link neighbors to the canonical vertices. A connection whose peer has no
	// vertex is simply not linked (should not happen since we indexed both edges).
	for _, v := range g.graph {
		for neighborPK := range v.connections {
			if nv, ok := g.graph[neighborPK]; ok {
				v.neighbors[neighborPK] = nv
			}
		}
	}

	return g
}
