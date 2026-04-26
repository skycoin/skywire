package store

import (
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

// edgeEntriesCache memoizes parsed *transport.Entry slices keyed by edge
// pubkey. Cache hits skip the SMEMBERS+MGET+JSON-unmarshal+pubkey-parse
// round-trip in GetTransportsByEdge.
//
// mirrorEdges fires GetTransportsByEdge once per touched edge after every
// register / deregister. Hub edges (edges that participate in many
// transports) are re-fetched every time another mutation touches them,
// even though the list often hasn't changed since the previous call.
// pprof on prod TPD showed mirrorEdges → GetTransportsByEdge dominating
// CPU at ~35 % cumulative even after PRs #2334–#2336.
//
// Mutations (RegisterTransport, RegisterTransportsBatch,
// DeregisterTransport) MUST call Invalidate for every touched edge so
// the next GetTransportsByEdge picks up the post-mutation list. The
// short TTL is a safety backstop only — correctness comes from explicit
// invalidation.
type edgeEntriesCache struct {
	mu    sync.RWMutex
	cap   int
	ttl   time.Duration
	items map[cipher.PubKey]edgeCacheEntry
}

type edgeCacheEntry struct {
	entries []*transport.Entry
	expiry  time.Time
}

const (
	defaultEdgeEntriesCacheCap = 4096
	defaultEdgeEntriesCacheTTL = 5 * time.Second
)

func newEdgeEntriesCache(cap int, ttl time.Duration) *edgeEntriesCache {
	if cap <= 0 {
		cap = defaultEdgeEntriesCacheCap
	}
	if ttl <= 0 {
		ttl = defaultEdgeEntriesCacheTTL
	}
	return &edgeEntriesCache{
		cap:   cap,
		ttl:   ttl,
		items: make(map[cipher.PubKey]edgeCacheEntry, cap),
	}
}

// Get returns the cached entries for an edge if the cache holds an
// unexpired entry for it. The returned slice is the cache's own backing
// slice — callers must not modify it.
func (c *edgeEntriesCache) Get(pk cipher.PubKey) ([]*transport.Entry, bool) {
	c.mu.RLock()
	e, ok := c.items[pk]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expiry) {
		return nil, false
	}
	return e.entries, true
}

// Put stores entries for an edge with the cache's configured TTL.
// Random eviction at capacity — the cache is a perf aid, not
// correctness-critical (TTL bounds staleness; explicit Invalidate
// handles correctness across mutations).
func (c *edgeEntriesCache) Put(pk cipher.PubKey, entries []*transport.Entry) {
	c.mu.Lock()
	if len(c.items) >= c.cap {
		for k := range c.items {
			delete(c.items, k)
			break
		}
	}
	c.items[pk] = edgeCacheEntry{
		entries: entries,
		expiry:  time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}

// Invalidate drops any cached entry for the given edges. Mutators call
// this for every touched edge before mirrorEdges runs so the next
// GetTransportsByEdge sees the post-mutation state.
func (c *edgeEntriesCache) Invalidate(pks ...cipher.PubKey) {
	if len(pks) == 0 {
		return
	}
	c.mu.Lock()
	for _, pk := range pks {
		delete(c.items, pk)
	}
	c.mu.Unlock()
}
