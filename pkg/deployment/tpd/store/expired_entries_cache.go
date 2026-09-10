// Package store pkg/deployment/tpd/store/expired_entries_cache.go c4-net-discovery
package store

import (
	"sync"
	"time"
)

// bwIndexTTL bounds how long one SCAN-derived bandwidth day index is reused
// before it is rebuilt. Which (transport, day) hashes exist changes on the
// cadence of a transport's first bandwidth report of a day, and the index
// treats today and yesterday as "may have appeared since the scan" anyway
// (see bwDayIndex.fetchDays), so a few-minute TTL costs nothing in
// correctness: only a transport newly aged out of the registered set can
// show up late, by at most one TTL.
//
// The index exists because the CXO metrics publisher
// (pkg/deployment/tpd/api/cxo_metrics_publisher.go) calls
// GetAllTransportMetrics on a ticker (1 day every 60 s, the full 30-day
// window every 30 min). Before it, every full cycle SCANned the whole
// keyspace once per day in the window (35 passes over ~3M keys, ~70 s of
// redis CPU), fetched each candidate's edge pair with an unpipelined GET
// (~270k round trips) and then HGETALLed every (transport × day) key of the
// window whether or not it existed — 8.4M HGETALLs of which 92% missed.
const bwIndexTTL = 5 * time.Minute

// bwIndexCache memoizes the registered-INDEPENDENT bandwidth day index. The
// caller's `registered` filter is applied fresh on every use (see
// expiredTransportEntries) so a transport that just (re)registered is dropped
// immediately and never reported as expired for up to the TTL.
type bwIndexCache struct {
	mu  sync.Mutex
	ttl time.Duration
	ix  *bwDayIndex
}

func newBWIndexCache(ttl time.Duration) *bwIndexCache {
	if ttl <= 0 {
		ttl = bwIndexTTL
	}
	return &bwIndexCache{ttl: ttl}
}

// get returns the cached index if present and unexpired.
func (c *bwIndexCache) get() (*bwDayIndex, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ix == nil || time.Since(c.ix.scannedAt) > c.ttl {
		return nil, false
	}
	return c.ix, true
}

// peek returns whatever index is cached, stale or not, or nil. Readers that
// only use it to skip keys the scan proved absent can tolerate a stale index:
// a key can only appear for today (or yesterday across a UTC rollover), and
// fetchDays always includes those two days.
func (c *bwIndexCache) peek() *bwDayIndex {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ix
}

func (c *bwIndexCache) put(ix *bwDayIndex) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ix = ix
}
