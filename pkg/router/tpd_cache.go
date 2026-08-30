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

// Package router pkg/router/tpd_cache.go c2-net-routing
package router

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
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
//
// byEdge / latencyByID / typeByID / throughputByID are derived lookups
// materialized ONCE per snapshot refresh. calculateLocalRoutes used to
// rebuild all four from the ~16k-entry set on every call; on a NAT'd
// visor with no direct transport to the destination (e.g. the in-browser
// wasm visor) that early-return is skipped, so every browse dial paid
// four full-dataset map builds + a per-edge sort. Under a route-setup
// retry loop that pegged the single js/wasm thread in mallocgc/GC. These
// are immutable once built, so serving them from the snapshot is
// identical output at a fraction of the allocation.
type tpdSnapshot struct {
	entries []*transport.Entry
	byID    map[uuid.UUID]*transport.Entry
	// byEdge maps a pubkey to the (non-setup) transports touching it,
	// each edge list sorted by type preference (direct types before DMSG).
	byEdge map[cipher.PubKey][]*transport.Entry
	// per-TpID metrics, hydrated by TPD's CXO telemetry aggregator.
	latencyByID    map[uuid.UUID]float64
	typeByID       map[uuid.UUID]string
	throughputByID map[uuid.UUID]float64
	// version is the CXO snapshot timestamp this snapshot was built from
	// (zero when built off the wall-clock TTL path, e.g. a non-CXO
	// discovery client). When set, the cache reuses this snapshot until
	// the source reports a newer timestamp — CXO-cadence invalidation
	// rather than an independent timer.
	version time.Time
	// expires is the wall-clock TTL floor, always set. It governs the
	// non-CXO path and bounds staleness if the version probe later stops
	// answering (CXO feed dropped) so the cache can't pin forever.
	expires time.Time
}

// tpdVersioner is implemented by a discovery client whose GetAllTransports
// snapshot is backed by the visor's event-driven CXO subscription and can
// report when that snapshot last advanced. When the client implements it,
// the cache invalidates on the reported timestamp (CXO's own sync cadence)
// instead of a separate 5-minute wall clock that can drift out of phase
// with it; clients that don't (plain HTTP, tests) keep the TTL.
type tpdVersioner interface {
	AllTransportsSyncedAt() (time.Time, bool)
}

// versionProbe returns dc's CXO-snapshot-timestamp probe, or nil when dc
// can't report one (so the caller falls back to the TTL).
func versionProbe(dc interface{}) func() (time.Time, bool) {
	if v, ok := dc.(tpdVersioner); ok {
		return v.AllTransportsSyncedAt
	}
	return nil
}

// deriveTransportLookups materializes the by-edge and per-TpID metric
// maps used by local route calculation. Setup-labeled transports are
// excluded from byEdge (RSN control-plane only); each edge list is
// sorted by type preference so callers try direct types before DMSG.
func deriveTransportLookups(entries []*transport.Entry) (
	byEdge map[cipher.PubKey][]*transport.Entry,
	latencyByID map[uuid.UUID]float64,
	typeByID map[uuid.UUID]string,
	throughputByID map[uuid.UUID]float64,
) {
	byEdge = make(map[cipher.PubKey][]*transport.Entry)
	latencyByID = make(map[uuid.UUID]float64)
	typeByID = make(map[uuid.UUID]string, len(entries))
	throughputByID = make(map[uuid.UUID]float64)
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		typeByID[entry.ID] = string(entry.Type)
		if entry.Latency > 0 {
			latencyByID[entry.ID] = entry.Latency
		}
		if entry.ThroughputBps > 0 {
			throughputByID[entry.ID] = entry.ThroughputBps
		}
		if entry.Label == transport.LabelSetup {
			continue
		}
		for _, edge := range entry.Edges {
			byEdge[edge] = append(byEdge[edge], entry)
		}
	}
	for edge := range byEdge {
		es := byEdge[edge]
		sort.SliceStable(es, func(i, j int) bool {
			return tptypes.TypePreference(es[i].Type) <
				tptypes.TypePreference(es[j].Type)
		})
	}
	return byEdge, latencyByID, typeByID, throughputByID
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

// buildSnapshot materializes a snapshot (and its derived lookups) from a
// freshly-fetched entry set, stamping it with the CXO version (zero on the
// TTL path) and always setting the wall-clock TTL floor.
func (c *tpdSnapshotCache) buildSnapshot(entries []*transport.Entry, version time.Time) *tpdSnapshot {
	byID := make(map[uuid.UUID]*transport.Entry, len(entries))
	for _, e := range entries {
		if e != nil {
			byID[e.ID] = e
		}
	}
	byEdge, latencyByID, typeByID, throughputByID := deriveTransportLookups(entries)
	return &tpdSnapshot{
		entries:        entries,
		byID:           byID,
		byEdge:         byEdge,
		latencyByID:    latencyByID,
		typeByID:       typeByID,
		throughputByID: throughputByID,
		version:        version,
		expires:        c.clock().Add(c.ttl),
	}
}

// snapshot returns a fresh transport-discovery snapshot, fetching a new
// one via fetchAll only when necessary. On a fetch error the previous
// snapshot is returned stale (with the error) so callers can proceed on
// slightly-old data rather than fail the dial; only when there is no prior
// snapshot at all does it return (nil, err).
//
// When version is non-nil and reports ok, the cache is CXO-driven: it
// serves the cached snapshot as long as the reported timestamp is
// unchanged and refetches exactly when it advances — no wall-clock
// refresh, so route data tracks the visor's CXO sync cadence rather than
// an independent 5-minute clock. When version is nil or reports !ok
// (non-CXO client, or the feed isn't primed yet) it falls back to the TTL.
//
// fetchAll is typically DiscoveryClient.GetAllTransports.
func (c *tpdSnapshotCache) snapshot(
	ctx context.Context,
	fetchAll func(context.Context) ([]*transport.Entry, error),
	version func() (time.Time, bool),
) (*tpdSnapshot, error) {
	// CXO-driven path: serve until the source's snapshot timestamp moves
	// — but keep the TTL as a liveness floor. The CXO transport feed is
	// refcount-gated (TabCloseGrace ~10s): once route-calc stops holding
	// it the sync cycle stops and lastSyncAt FREEZES (the snapshot is not
	// cleared). Cache hits here deliberately don't re-acquire the feed, so
	// without the floor a stable-but-frozen version would pin this cache
	// to an old snapshot forever and transports would never refresh. The
	// floor forces a refetch every TTL even when the version hasn't moved;
	// that refetch goes through GetAllTransports, which re-acquires the
	// feed (restarting its cycle) and picks up anything new. So: refresh
	// immediately when CXO advances, and at worst once per TTL when CXO is
	// idle — never per call.
	if version != nil {
		if ts, ok := version(); ok {
			c.mu.RLock()
			cur := c.snap
			c.mu.RUnlock()
			if cur != nil && !cur.version.IsZero() && cur.version.Equal(ts) && c.clock().Before(cur.expires) {
				return cur, nil
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.snap != nil && !c.snap.version.IsZero() && c.snap.version.Equal(ts) && c.clock().Before(c.snap.expires) {
				return c.snap, nil
			}
			entries, err := fetchAll(ctx)
			if err != nil {
				if c.snap != nil {
					return c.snap, err
				}
				return nil, err
			}
			c.snap = c.buildSnapshot(entries, ts)
			return c.snap, nil
		}
	}

	// TTL fallback (non-CXO client, or CXO feed not primed yet).
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

	c.snap = c.buildSnapshot(entries, time.Time{})
	return c.snap, nil
}
