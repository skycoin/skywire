package store

import (
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// entryCache is a small in-process TTL cache in front of Redis for
// GET /dmsg-discovery/entry/{pk} lookups. At production load a handful
// of popular PKs (dmsg servers, active visors) account for most of
// dmsg-discovery's read traffic; a short-TTL positive cache absorbs
// those without adding staleness relative to the visor entry refresh
// cadence (~30s). Writes (SetEntry/DelEntry) invalidate the cached
// key so updates are immediately visible.
//
// The cache is positive-only: misses are NOT cached. Visor code has a
// separate fix that seeds synthetic entries for direct-client service
// PKs, so the 404 storm they previously caused is eliminated at the
// source, not papered over here.
type entryCache struct {
	mu    sync.RWMutex
	items map[cipher.PubKey]entryCacheItem
	max   int
	ttl   time.Duration
}

type entryCacheItem struct {
	entry *disc.Entry
	exp   time.Time
}

func newEntryCache(max int, ttl time.Duration) *entryCache {
	return &entryCache{
		items: make(map[cipher.PubKey]entryCacheItem, max),
		max:   max,
		ttl:   ttl,
	}
}

// get returns the cached entry and true iff it exists and is within TTL.
func (c *entryCache) get(pk cipher.PubKey) (*disc.Entry, bool) {
	c.mu.RLock()
	it, ok := c.items[pk]
	c.mu.RUnlock()
	if !ok || time.Now().After(it.exp) {
		return nil, false
	}
	return it.entry, true
}

// set stores the entry with the cache's TTL. When the map is at
// capacity we drop any expired items we find in a single scan; if none
// are expired we evict one arbitrary entry to make room. This keeps
// the operation bounded and avoids pulling in a full LRU dependency
// for what is effectively a TTL-bounded hot-key cache.
func (c *entryCache) set(pk cipher.PubKey, entry *disc.Entry) {
	if entry == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.items[pk]; !exists && len(c.items) >= c.max {
		now := time.Now()
		evicted := false
		for k, v := range c.items {
			if now.After(v.exp) {
				delete(c.items, k)
				evicted = true
			}
		}
		if !evicted {
			for k := range c.items {
				delete(c.items, k)
				break
			}
		}
	}
	c.items[pk] = entryCacheItem{entry: entry, exp: time.Now().Add(c.ttl)}
}

// invalidate drops the cached entry for pk, if any.
func (c *entryCache) invalidate(pk cipher.PubKey) {
	c.mu.Lock()
	delete(c.items, pk)
	c.mu.Unlock()
}
