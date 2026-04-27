package store

import (
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/transport"
)

// allTransportsCache memoizes the result of GetAllTransports and
// getAllTransportsWithQoS for a short TTL. The WithQoS variant is hit
// on every metrics scrape (Prometheus / Victoria Metrics pulling
// /metrics/network, /metrics/visors, etc.); both do a Redis SCAN over
// the full tp:* keyspace plus an MGET of every transport key.
//
// The cache holds four slots: {selfTransports} × {withQoS}. Within the
// TTL window, all callers of a given variant share the same snapshot.
// There is no explicit invalidation; the snapshot can be up to TTL
// stale, which matches the consistency model these callers already
// accept (metrics scrapers tolerate any sub-scrape-interval delay).
type allTransportsCache struct {
	mu  sync.RWMutex
	ttl time.Duration

	withSelf       allTransportsCacheEntry
	withoutSelf    allTransportsCacheEntry
	withSelfQoS    allTransportsCacheEntry
	withoutSelfQoS allTransportsCacheEntry
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

// Get returns the cached entries for the given filters if the slot
// holds an unexpired snapshot. The returned slice is the cache's own
// backing array — callers must not modify it.
func (c *allTransportsCache) Get(selfTransports, withQoS bool) ([]*transport.Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e := c.slot(selfTransports, withQoS)
	if e.entries == nil || time.Now().After(e.expiry) {
		return nil, false
	}
	return e.entries, true
}

// Put stores entries for the given filters with the cache's configured TTL.
func (c *allTransportsCache) Put(selfTransports, withQoS bool, entries []*transport.Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := allTransportsCacheEntry{entries: entries, expiry: time.Now().Add(c.ttl)}
	switch {
	case selfTransports && withQoS:
		c.withSelfQoS = e
	case !selfTransports && withQoS:
		c.withoutSelfQoS = e
	case selfTransports && !withQoS:
		c.withSelf = e
	default:
		c.withoutSelf = e
	}
}

func (c *allTransportsCache) slot(selfTransports, withQoS bool) allTransportsCacheEntry {
	switch {
	case selfTransports && withQoS:
		return c.withSelfQoS
	case !selfTransports && withQoS:
		return c.withoutSelfQoS
	case selfTransports && !withQoS:
		return c.withSelf
	default:
		return c.withoutSelf
	}
}
