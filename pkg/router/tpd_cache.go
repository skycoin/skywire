// pkg/router/tpd_cache.go — visor-wide TTL cache for TPD
// GetTransportByID lookups, so buildHopLookups (and any future
// caller that needs per-hop transport metadata) doesn't hammer the
// TPD HTTP rate limit.
//
// Motivation: skynet-client --routes N --min-hops K causes the
// dial path to call buildHopLookups with multiple multi-hop
// candidates (typically 3 forward × M hops + 3 reverse × M hops =
// 12+ unique TpIDs per dial). Each retry round hits buildHopLookups
// again. With TPD enforcing 30 req/min, even a single dial with
// retries exceeds the budget and every transport lookup returns
// "429 Too Many Requests: Rate limit exceeded" → buildHopLookups
// silently degrades (it logs at Debug + continues with empty
// latency/type for that hop) → downstream path-pick filters can't
// reject DMSG-multihop or rank by latency → the route group fails
// to assemble end-to-end.
//
// The cache is purely a local read-side optimization: the same TPD
// entry is reusable for many dial attempts as long as it's fresh.
// We don't write back to TPD; entries expire naturally and the
// next read re-fetches.
//
// TTL is sized to outlast a single dial's retry burst (3 retries
// × ~10s per attempt) while still being short enough that a
// genuinely-deleted-transport entry doesn't linger long enough to
// cause downstream mis-pick. 60s is a comfortable middle.
//
// Negative caching: failed lookups are cached briefly too so that
// a transient TPD 429 doesn't trigger an instant retry storm. The
// negative TTL is short (5s) — short enough for a TPD recovery
// window to be respected, long enough to break a retry tight loop.

package router

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/transport"
)

// tpdEntryCache is a small TTL cache of TPD GetTransportByID
// results. Concurrent-safe via sync.RWMutex (lookups are read-heavy
// + bursty; cache fill is the only writer per ID).
type tpdEntryCache struct {
	mu      sync.RWMutex
	entries map[uuid.UUID]tpdCacheEntry
	posTTL  time.Duration
	negTTL  time.Duration
	clock   func() time.Time // injectable for tests
}

// tpdCacheEntry is one cached lookup result. When entry is nil
// the lookup failed (rate-limited or 404); the negative TTL
// suppresses retries until expiry.
type tpdCacheEntry struct {
	entry   *transport.Entry // nil ⇒ negative cache
	expires time.Time
}

// defaultTPDCachePosTTL is the lifetime of a cached successful
// GetTransportByID result. TPD entries rarely change within a
// minute (the publisher only re-registers on state change), so a
// 60s TTL is well within the freshness budget for routing
// decisions.
const defaultTPDCachePosTTL = 60 * time.Second

// defaultTPDCacheNegTTL is the lifetime of a cached failure (rate
// limit, 404, network error). Short enough that a TPD recovery is
// noticed within seconds; long enough to break a retry tight loop
// (e.g. the dial path's three-attempt outer loop).
const defaultTPDCacheNegTTL = 5 * time.Second

// newTPDEntryCache returns an empty cache with default TTLs.
func newTPDEntryCache() *tpdEntryCache {
	return &tpdEntryCache{
		entries: make(map[uuid.UUID]tpdCacheEntry),
		posTTL:  defaultTPDCachePosTTL,
		negTTL:  defaultTPDCacheNegTTL,
		clock:   time.Now,
	}
}

// Get returns (entry, true, true) on positive cache hit, (nil,
// true, false) on negative cache hit (cached failure within negTTL),
// and (nil, false, false) on cache miss. The third return distinguishes
// "we have a cached *good* entry" from "we have a cached failure" so
// callers know whether to skip the TPD round-trip.
func (c *tpdEntryCache) Get(id uuid.UUID) (entry *transport.Entry, hit bool, positive bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[id]
	if !ok {
		return nil, false, false
	}
	if c.clock().After(e.expires) {
		return nil, false, false
	}
	if e.entry == nil {
		return nil, true, false
	}
	return e.entry, true, true
}

// PutSuccess records a successful TPD lookup. Subsequent Get calls
// within posTTL return entry without hitting TPD.
func (c *tpdEntryCache) PutSuccess(id uuid.UUID, entry *transport.Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[id] = tpdCacheEntry{
		entry:   entry,
		expires: c.clock().Add(c.posTTL),
	}
}

// PutFailure records a failed TPD lookup (rate-limited, 404, etc.).
// Subsequent Get calls within negTTL return (nil, true, false), so
// the caller can skip the TPD round-trip and treat the ID as
// "unknown" for the negative-cache window.
func (c *tpdEntryCache) PutFailure(id uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[id] = tpdCacheEntry{
		entry:   nil,
		expires: c.clock().Add(c.negTTL),
	}
}

// GetOrFetch returns the cached entry if fresh; otherwise calls
// fetch and caches the result (positive or negative). The fetch
// function is invoked exactly once per cache miss per goroutine —
// concurrent misses for the same id MAY each call fetch (sync.Map's
// LoadOrStore-style dedup would add complexity for marginal gain
// since the underlying TPD client already de-dups identical
// in-flight requests at the HTTP layer).
//
// On fetch error the cache records the failure (negative TTL) and
// returns (nil, err) so the caller can apply its own fallback (in
// buildHopLookups: log at Debug + continue with empty lookup for
// that hop).
func (c *tpdEntryCache) GetOrFetch(
	ctx context.Context,
	id uuid.UUID,
	fetch func(context.Context, uuid.UUID) (*transport.Entry, error),
) (*transport.Entry, error) {
	if entry, hit, positive := c.Get(id); hit {
		if positive {
			return entry, nil
		}
		// Negative cache hit — skip the TPD round-trip; the
		// caller treats this the same as a fresh failure.
		return nil, errTPDCachedFailure
	}
	entry, err := fetch(ctx, id)
	if err != nil {
		c.PutFailure(id)
		return nil, err
	}
	c.PutSuccess(id, entry)
	return entry, nil
}

// errTPDCachedFailure is the sentinel error returned by GetOrFetch
// when a recent failure is still in the negative cache. Callers
// can errors.Is-match this to distinguish a fresh TPD call failure
// from a suppressed retry.
var errTPDCachedFailure = tpdCachedFailureError{}

type tpdCachedFailureError struct{}

func (tpdCachedFailureError) Error() string {
	return "tpd: skipped (negative cache hit within window)"
}
