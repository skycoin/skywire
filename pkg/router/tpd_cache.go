// pkg/router/tpd_cache.go — a small TTL cache holding ONE snapshot of
// the whole transport-discovery dataset (the result of a single
// GetAllTransports call), shared by the router's route-calculation
// paths.
//
// Motivation: route calculation is a local operation over the
// transport graph, so the visor needs the transport set in hand
// anyway. calculateLocalRoutes already fetches it in one bulk
// GetAllTransports call; buildHopLookups (the latency-rank + DMSG
// filter for the route-finder path) previously re-derived the same
// data with one GetTransportByID HTTP call *per hop ID*. On a
// multiplexed multi-hop dial that's 12+ IDs × retries — easily past
// TPD's 30-req/min rate limit, so every per-hop lookup returns
// "429 Too Many Requests", buildHopLookups silently degrades to empty
// latency/type, and the route group never assembles.
//
// The fix is structural, not a per-ID band-aid: fetch the whole set
// ONCE, cache it with a short TTL, and serve every hop lookup from the
// in-memory snapshot. TPD entries only change on transport
// register/deregister, so a snapshot is fresh enough for routing
// decisions for minutes; 5m is a comfortable middle that keeps a
// genuinely-removed transport from lingering long enough to mis-route.
//
// This is purely a local read-side cache — we never write back to TPD.
// On a refresh error the previous snapshot is served stale rather than
// dropping to empty, so a transient TPD blip can't break routing.
//
// Longer term the snapshot source should be the on-demand CXO
// subscriber to TPD (the transport manager already mirrors
// register/deregister into a CXO publisher tree) rather than an HTTP
// GetAllTransports round-trip — at which point this cache becomes a
// thin adapter over the CXO-backed store.

package router

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/transport"
)

// defaultTPDSnapshotTTL is how long a GetAllTransports snapshot is
// served before a refresh. TPD entries are republished on transport
// state change, so minutes-scale staleness is acceptable for routing.
const defaultTPDSnapshotTTL = 5 * time.Minute

// tpdSnapshotCache caches one whole-dataset GetAllTransports result.
// Concurrent-safe: readers take the RLock to grab the current
// immutable snapshot; a refresh builds a NEW snapshot and swaps the
// pointer under the write lock, so a snapshot handed to a caller is
// never mutated underneath it.
type tpdSnapshotCache struct {
	mu    sync.RWMutex
	snap  *tpdSnapshot
	ttl   time.Duration
	clock func() time.Time // injectable for tests
}

// tpdSnapshot is one immutable view of the transport-discovery set.
type tpdSnapshot struct {
	entries []*transport.Entry
	byID    map[uuid.UUID]*transport.Entry
	expires time.Time
}

// newTPDSnapshotCache returns an empty cache with the default TTL.
func newTPDSnapshotCache() *tpdSnapshotCache {
	return &tpdSnapshotCache{
		ttl:   defaultTPDSnapshotTTL,
		clock: time.Now,
	}
}

// fresh returns the current snapshot if it exists and hasn't expired.
func (c *tpdSnapshotCache) fresh() *tpdSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.snap != nil && c.clock().Before(c.snap.expires) {
		return c.snap
	}
	return nil
}

// snapshot returns a fresh transport-discovery snapshot, fetching a
// new one via fetchAll only when the cache is empty or expired. On a
// fetch error the previous snapshot is returned stale (with the error)
// so callers can choose to proceed on slightly-old data rather than
// fail the dial; only when there is no prior snapshot at all does it
// return (nil, err).
//
// fetchAll is typically DiscoveryClient.GetAllTransports.
func (c *tpdSnapshotCache) snapshot(
	ctx context.Context,
	fetchAll func(context.Context) ([]*transport.Entry, error),
) (*tpdSnapshot, error) {
	if s := c.fresh(); s != nil {
		return s, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// Double-check: another goroutine may have refreshed while we
	// waited for the write lock.
	if c.snap != nil && c.clock().Before(c.snap.expires) {
		return c.snap, nil
	}

	entries, err := fetchAll(ctx)
	if err != nil {
		// Serve the prior snapshot stale rather than dropping to
		// empty — a transient TPD failure shouldn't break routing.
		if c.snap != nil {
			return c.snap, err
		}
		return nil, err
	}

	byID := make(map[uuid.UUID]*transport.Entry, len(entries))
	for _, e := range entries {
		if e != nil {
			byID[e.ID] = e
		}
	}
	c.snap = &tpdSnapshot{
		entries: entries,
		byID:    byID,
		expires: c.clock().Add(c.ttl),
	}
	return c.snap, nil
}
