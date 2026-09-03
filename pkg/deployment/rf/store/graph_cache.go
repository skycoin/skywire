// Package store pkg/deployment/rf/store/graph_cache.go c2-net-routing
package store

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
)

// GraphCache holds ONE full-network graph, shared across every route request, and
// rebuilds it in the background on a fixed cadence. This removes the per-request,
// per-source graph construction the route-finder did before — a redis walk that
// issued a GetTransportsByEdge for every node it discovered from the source,
// which pegged both the route-finder and redis under load. The cached graph is
// read-only during a BFS (StreamRoutes never mutates it), so many requests can
// share it concurrently; a rebuild constructs a fresh graph and swaps the pointer
// atomically, leaving in-flight requests on the previous one until they finish.
//
// The rebuild is intentionally unconditional per tick rather than diff-gated:
// building from a (store-cached) GetAllTransports read is O(transports) and takes
// a few milliseconds, and rebuilding every tick keeps per-edge latency fresh for
// latency-weighted routing. DefaultCacheRefresh bounds staleness.
type GraphCache struct {
	store   store.Store
	refresh time.Duration
	log     logrus.FieldLogger
	cur     atomic.Pointer[Graph]
}

// DefaultCacheRefresh is the graph-cache rebuild cadence when none is given.
const DefaultCacheRefresh = 15 * time.Second

// NewGraphCache returns an unstarted cache. Call Run (usually in a goroutine) to
// keep it warm, or Rebuild once for a synchronous build. Until the first build
// completes Get returns nil and callers should fall back to a per-request build.
func NewGraphCache(s store.Store, refresh time.Duration, log logrus.FieldLogger) *GraphCache {
	if refresh <= 0 {
		refresh = DefaultCacheRefresh
	}
	return &GraphCache{store: s, refresh: refresh, log: log}
}

// Get returns the current cached full graph, or nil before the first build.
func (c *GraphCache) Get() *Graph { return c.cur.Load() }

// Rebuild reads the transport set once and swaps in a freshly built full graph.
// On a read error the previous graph is kept and the error returned.
func (c *GraphCache) Rebuild(ctx context.Context) (*Graph, error) {
	entries, err := allTransportsForGraph(ctx, c.store)
	if err != nil {
		return c.cur.Load(), err
	}
	g := graphFromEntries(c.store, entries)
	c.cur.Store(g)
	if c.log != nil {
		c.log.Debugf("route-finder graph cache rebuilt: %d nodes from %d transports", len(g.graph), len(entries))
	}
	return g, nil
}

// Run builds the graph immediately, then rebuilds every refresh interval until
// ctx is done. A rebuild error keeps the previous graph and is logged, not fatal.
func (c *GraphCache) Run(ctx context.Context) {
	if _, err := c.Rebuild(ctx); err != nil && c.log != nil {
		c.log.WithError(err).Warn("route-finder graph cache: initial build failed; requests fall back to per-source build")
	}
	t := time.NewTicker(c.refresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := c.Rebuild(ctx); err != nil && c.log != nil {
				c.log.WithError(err).Debug("route-finder graph cache: rebuild failed; keeping previous graph")
			}
		}
	}
}
