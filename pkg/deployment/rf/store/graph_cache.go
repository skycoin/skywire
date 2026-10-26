// Package store pkg/deployment/rf/store/graph_cache.go c2-net-routing
package store

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
)

// GraphCache holds ONE full-network graph, shared across every route request.
// This removes the per-request, per-source graph construction the route-finder
// did before — a redis walk that issued a GetTransportsByEdge for every node it
// discovered from the source, which pegged both the route-finder and redis under
// load. The cached graph is read-only during a BFS (StreamRoutes never mutates
// it), so many requests can share it concurrently; a rebuild constructs a fresh
// graph and swaps the pointer atomically, leaving in-flight requests on the
// previous one until they finish.
//
// Once started, the graph is rebuilt when a request finds it older than the
// refresh interval, not on a timer. Rebuilding every 15 s over 93k transports
// while serving about 10 queries a minute was most of the route finder's CPU
// and allocation (audit 2026-10-16). A request that finds the graph stale is
// answered from it while one rebuild runs in the background; only a graph
// older than staleAfter is rebuilt before answering, so an idle route finder
// does not route on an old graph.
type GraphCache struct {
	store store.Store
	// sharedURL names the redis whose live transport set, held by a TPD in
	// this process, the graph is built from instead of redis
	// (store.SharedLiveStore). Empty: always read store.
	sharedURL string
	refresh   time.Duration
	log       logrus.FieldLogger
	cur       atomic.Pointer[Graph]

	// builtAt is when cur was built, in Unix nanoseconds.
	builtAt atomic.Int64
	// failedAt is when a rebuild last failed, in Unix nanoseconds.
	failedAt atomic.Int64
	// ctx is the cache's lifetime, set by Run; nil until started.
	ctx atomic.Pointer[context.Context]
	// mu makes rebuilds single-flight; building marks one in the background.
	mu       sync.Mutex
	building atomic.Bool
}

// DefaultCacheRefresh is how old the graph may get before a request triggers a
// rebuild, when none is given.
const DefaultCacheRefresh = 15 * time.Second

// staleFactor times the refresh interval is the age past which a request waits
// for the rebuild instead of using the old graph.
const staleFactor = 4

// NewGraphCache returns an unstarted cache. Call Run (usually in a goroutine) to
// build it and let requests keep it fresh, or Rebuild once for a synchronous
// build. Until the first build completes Get returns nil and callers should fall
// back to a per-request build.
func NewGraphCache(s store.Store, refresh time.Duration, log logrus.FieldLogger) *GraphCache {
	if refresh <= 0 {
		refresh = DefaultCacheRefresh
	}
	return &GraphCache{store: s, refresh: refresh, log: log}
}

// Get returns the current full graph, or nil before the first build. On a
// started cache a graph older than the refresh interval is rebuilt: in the
// background, or first when it is older than staleFactor intervals.
func (c *GraphCache) Get() *Graph {
	g := c.cur.Load()
	ctxp := c.ctx.Load()
	if ctxp == nil {
		return g
	}
	age := time.Since(time.Unix(0, c.builtAt.Load()))
	if g != nil && age < c.refresh {
		return g
	}
	if g == nil || age >= staleFactor*c.refresh {
		return c.rebuildIfStale(*ctxp)
	}
	if c.building.CompareAndSwap(false, true) {
		go func() {
			defer c.building.Store(false)
			c.rebuildIfStale(*ctxp)
		}()
	}
	return g
}

// Current returns the graph as last built, never rebuilding it.
func (c *GraphCache) Current() *Graph { return c.cur.Load() }

// rebuildIfStale rebuilds unless another caller did while this one waited.
func (c *GraphCache) rebuildIfStale(ctx context.Context) *Graph {
	c.mu.Lock()
	defer c.mu.Unlock()
	if g := c.cur.Load(); g != nil && time.Since(time.Unix(0, c.builtAt.Load())) < c.refresh {
		return g
	}
	// A store that just failed is not asked again by every request.
	if time.Since(time.Unix(0, c.failedAt.Load())) < c.refresh {
		return c.cur.Load()
	}
	g, err := c.Rebuild(ctx)
	if err != nil {
		c.failedAt.Store(time.Now().UnixNano())
	}
	if err != nil && c.log != nil {
		c.log.WithError(err).Debug("route-finder graph cache: rebuild failed; keeping previous graph")
	}
	return g
}

// ShareFrom makes the cache build from the live transport set of a TPD in
// this process that writes to the redis at url, when there is one — no
// redis read per rebuild. Call before Run.
func (c *GraphCache) ShareFrom(url string) { c.sharedURL = url }

// source is the store a rebuild reads: a co-hosted TPD's, else its own.
func (c *GraphCache) source() store.Store {
	if c.sharedURL != "" {
		if s, ok := store.SharedLiveStore(c.sharedURL); ok {
			return s
		}
	}
	return c.store
}

// Rebuild reads the transport set once and swaps in a freshly built full graph.
// On a read error the previous graph is kept and the error returned.
func (c *GraphCache) Rebuild(ctx context.Context) (*Graph, error) {
	src := c.source()
	entries, err := allTransportsForGraph(ctx, src)
	if err != nil {
		return c.cur.Load(), err
	}
	g := graphFromEntries(src, entries)
	if prev := c.cur.Load(); prev != nil {
		// Hand the landmark tables on (see landmarkMaxAge): the previous
		// graph's own build, or what it inherited if it never needed one.
		if g.inherited = prev.built.Load(); g.inherited == nil {
			g.inherited = prev.inherited
		}
	}
	c.cur.Store(g)
	c.builtAt.Store(time.Now().UnixNano())
	if c.log != nil {
		c.log.Debugf("route-finder graph cache rebuilt: %d nodes from %d transports", len(g.graph), len(entries))
	}
	return g, nil
}

// Run builds the graph immediately and starts the cache: from then on requests
// rebuild it when stale (see Get), until ctx is done. A failed initial build is
// logged, and the next request tries again.
func (c *GraphCache) Run(ctx context.Context) {
	c.mu.Lock()
	c.ctx.Store(&ctx)
	_, err := c.Rebuild(ctx)
	if err != nil {
		c.failedAt.Store(time.Now().UnixNano())
	}
	c.mu.Unlock()
	if err != nil && c.log != nil {
		c.log.WithError(err).Warn("route-finder graph cache: initial build failed; requests fall back to per-source build")
	}
	<-ctx.Done()
}
