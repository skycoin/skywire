// Package store pkg/deployment/tpd/store/expired_entries_cache.go c4-net-discovery
package store

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/transport"
)

// expiredEntriesCacheTTL bounds how long the SCAN-derived expired-transport
// candidate set is reused before it is recomputed. The expired set changes
// on a ~daily cadence (a transport ages out of the registered set), not
// every minute, so a few-minute TTL is safe: a newly-EXPIRED transport may
// appear up to one TTL late, which is acceptable.
//
// This cache exists because the CXO metrics publisher
// (pkg/deployment/tpd/api/cxo_metrics_publisher.go) turned
// expiredTransportEntries from a low-frequency path (rewards dashboard /
// daily reward calc) into a per-60s path: publishWindow calls
// GetAllTransportMetrics on a ticker (1d/60s, 7d/5m, 30d/30m), and each call
// SCANned the bw:daily:*:<date> keyspace once per day in the window (up to 35
// passes) over a ~13k-transport keyspace, then recovered edges per candidate.
// That put expiredTransportEntries at ~12% of TPD CPU with redis I/O
// dominating. Memoizing the registered-INDEPENDENT SCAN result collapses the
// repeated ticks onto one SCAN per TTL per window.
const expiredEntriesCacheTTL = 5 * time.Minute

// expiredEntriesCache memoizes, per day-window, the raw expired-transport
// candidate set derived purely from the redis SCAN + recoverBandwidthEdges.
// It deliberately does NOT bake in the caller's `registered` filter: that
// filter is applied fresh on every call (see expiredTransportEntries) so a
// transport that just (re)registered is dropped immediately and never counted
// as expired for up to the TTL. Keyed by `days` because the 1/7/30 windows
// scan different date ranges.
type expiredEntriesCache struct {
	mu     sync.Mutex
	ttl    time.Duration
	byDays map[int]expiredEntriesCacheEntry
}

type expiredEntriesCacheEntry struct {
	entries    []*transport.Entry
	expiredIDs map[uuid.UUID]bool
	cachedAt   time.Time
}

func newExpiredEntriesCache(ttl time.Duration) *expiredEntriesCache {
	if ttl <= 0 {
		ttl = expiredEntriesCacheTTL
	}
	return &expiredEntriesCache{ttl: ttl, byDays: make(map[int]expiredEntriesCacheEntry)}
}

// get returns the cached raw candidate set for the window if present and
// unexpired. The returned slice/map are the cache's own backing storage —
// callers must treat them as read-only.
func (c *expiredEntriesCache) get(days int) ([]*transport.Entry, map[uuid.UUID]bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byDays[days]
	if !ok || time.Since(e.cachedAt) > c.ttl {
		return nil, nil, false
	}
	return e.entries, e.expiredIDs, true
}

// put stores the raw candidate set for the window, stamped with the current
// time so get can treat entries older than the TTL as stale.
func (c *expiredEntriesCache) put(days int, entries []*transport.Entry, expiredIDs map[uuid.UUID]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byDays[days] = expiredEntriesCacheEntry{
		entries:    entries,
		expiredIDs: expiredIDs,
		cachedAt:   time.Now(),
	}
}
