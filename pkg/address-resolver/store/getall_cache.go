package store

import (
	"sync"
	"time"

	types "github.com/skycoin/skywire/pkg/transport/types"
)

// getAllCache memoizes the result of GetAll(netType) for a short TTL.
// AR's /transports endpoint calls GetAll twice per request (once for
// STCPR, once for SUDPH), and each call does a Redis SCAN COUNT=30000
// over the address-resolver:<type>:* keyspace.
//
// The cache holds two slots — one per netType. Within the TTL window,
// concurrent /transports callers share a single SCAN per netType. There
// is no explicit invalidation; callers of /transports tolerate the
// brief staleness (a recently-bound visor may take up to TTL seconds
// to appear in the list, same shape the consumers already accept from
// other discovery surfaces).
//
// Mirrors the per-edge cache pattern in pkg/transport-discovery/store
// (PRs #2340 and #2342).
type getAllCache struct {
	mu  sync.RWMutex
	ttl time.Duration

	stcpr getAllCacheEntry
	sudph getAllCacheEntry
}

type getAllCacheEntry struct {
	pks    []string
	expiry time.Time
}

const defaultGetAllCacheTTL = 5 * time.Second

func newGetAllCache(ttl time.Duration) *getAllCache {
	if ttl <= 0 {
		ttl = defaultGetAllCacheTTL
	}
	return &getAllCache{ttl: ttl}
}

// Get returns the cached PK list for netType if the slot holds an
// unexpired entry. The returned slice is the cache's own backing
// array — callers must not modify it.
func (c *getAllCache) Get(netType types.Type) ([]string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e := c.slot(netType)
	if e.pks == nil || time.Now().After(e.expiry) {
		return nil, false
	}
	return e.pks, true
}

// Put stores pks for netType with the cache's configured TTL.
func (c *getAllCache) Put(netType types.Type, pks []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := getAllCacheEntry{pks: pks, expiry: time.Now().Add(c.ttl)}
	switch netType {
	case types.STCPR:
		c.stcpr = e
	case types.SUDPH:
		c.sudph = e
	}
}

func (c *getAllCache) slot(netType types.Type) getAllCacheEntry {
	switch netType {
	case types.STCPR:
		return c.stcpr
	case types.SUDPH:
		return c.sudph
	}
	return getAllCacheEntry{}
}
