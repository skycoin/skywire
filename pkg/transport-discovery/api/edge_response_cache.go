package api

import (
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// edgeRespCacheTTL is how long a marshaled /transports/edge:<PK> body is
// reused before recompute. /transports/edge is the second-busiest TPD read
// endpoint after /all-transports — ~1300 calls/5min, called by every visor
// on a frequent poll cadence to learn its own and peers' transports. The
// short TTL keeps the per-edge list fresh enough for routing decisions
// while collapsing identical repeat reads into a single Redis round-trip.
const edgeRespCacheTTL = 3 * time.Second

// edgeRespCache memoizes the marshaled JSON body (and a gzip copy) of the
// /transports/edge:<PK> response per edge PK, single-flighting the recompute
// on miss and serving a stale body if the recompute errors. Expired slots
// are swept opportunistically on every miss so the map size tracks the set
// of edges actively being queried rather than all edges ever seen.
type edgeRespCache struct {
	ttl   time.Duration
	mu    sync.Mutex
	slots map[cipher.PubKey]*respCacheSlot
}

func newEdgeRespCache(ttl time.Duration) *edgeRespCache {
	if ttl <= 0 {
		ttl = edgeRespCacheTTL
	}
	return &edgeRespCache{ttl: ttl, slots: map[cipher.PubKey]*respCacheSlot{}}
}

// body returns the marshaled JSON and its gzip-compressed form for the edge,
// recomputing via compute() at most once per TTL. The lock is held across
// compute so concurrent misses for the same edge share a single recompute
// (single-flight). On compute error a still-cached (stale) body is returned
// when present; otherwise the error is propagated and nothing is cached.
func (c *edgeRespCache) body(edge cipher.PubKey, compute func() ([]byte, error)) (raw, gz []byte, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if slot := c.slots[edge]; slot != nil && now.Before(slot.expiresAt) {
		return slot.raw, slot.gz, nil
	}

	body, cerr := compute()
	if cerr != nil {
		if slot := c.slots[edge]; slot != nil { // stale-on-error
			return slot.raw, slot.gz, nil
		}
		return nil, nil, cerr
	}

	// Sweep expired slots on miss: bounds memory at the active-edge set
	// rather than the all-time-queried set, at O(N) per miss where N is
	// small (one slot per recently-queried edge).
	for k, s := range c.slots {
		if now.After(s.expiresAt) {
			delete(c.slots, k)
		}
	}

	gzBody := gzipBytes(body)
	c.slots[edge] = &respCacheSlot{
		raw:       body,
		gz:        gzBody,
		expiresAt: now.Add(c.ttl),
	}
	return body, gzBody, nil
}
