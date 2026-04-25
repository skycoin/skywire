package store

import (
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/transport"
)

// allTransportsCache memoizes the result of GetAllTransports for a short
// TTL. The function is hit on every register/deregister request that
// includes ?sync=true (visors asking for a fresh full snapshot to
// recompute local routes), and each call does a Redis SCAN over the
// full tp:* keyspace plus an MGET of every transport key. With ~50
// register/sec across the network, redis was spending ~390ms/sec in
// SCAN alone after PRs #2334–#2341 — call rate was 35/s × 11ms each.
//
// The cache holds at most two slots — one per value of the
// selfTransports filter. Within the TTL window, all sync=true callers
// share the same snapshot. There is no explicit invalidation; the
// snapshot can be up to TTL stale, which matches the consistency model
// sync=true callers already accept (the canonical fanout is the DHT
// mirror, which fires immediately on register; sync=true is a
// secondary snapshot path).
type allTransportsCache struct {
	mu  sync.RWMutex
	ttl time.Duration

	withSelf    allTransportsCacheEntry
	withoutSelf allTransportsCacheEntry
}

type allTransportsCacheEntry struct {
	entries []*transport.Entry
	expiry  time.Time
}

const defaultAllTransportsCacheTTL = 5 * time.Second

func newAllTransportsCache(ttl time.Duration) *allTransportsCache {
	if ttl <= 0 {
		ttl = defaultAllTransportsCacheTTL
	}
	return &allTransportsCache{ttl: ttl}
}

// Get returns the cached entries for the given selfTransports filter
// if the slot holds an unexpired snapshot. The returned slice is the
// cache's own backing array — callers must not modify it.
func (c *allTransportsCache) Get(selfTransports bool) ([]*transport.Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e := c.slot(selfTransports)
	if e.entries == nil || time.Now().After(e.expiry) {
		return nil, false
	}
	return e.entries, true
}

// Put stores entries for the given selfTransports filter with the
// cache's configured TTL.
func (c *allTransportsCache) Put(selfTransports bool, entries []*transport.Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := allTransportsCacheEntry{entries: entries, expiry: time.Now().Add(c.ttl)}
	if selfTransports {
		c.withSelf = e
	} else {
		c.withoutSelf = e
	}
}

func (c *allTransportsCache) slot(selfTransports bool) allTransportsCacheEntry {
	if selfTransports {
		return c.withSelf
	}
	return c.withoutSelf
}
