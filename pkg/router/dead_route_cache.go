// Package router pkg/router/dead_route_cache.go
package router

import (
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/skycoin/skywire/pkg/routing"
)

// A route group that dies almost immediately after it was dialed — the
// reciprocal handshake never arrives and saveRouteGroupRules gives up at
// handshakeAwaitTimeout (10s, router.go), or the peer closes the fresh group
// before it ever carried a byte — is evidence about THAT route, not about the
// dial. The ranker had no memory of it: every per-dial exclusion (opts
// ExcludeIntermediates / ExcludeTransportIDs) is discarded with the DialOptions,
// so a pool fill that dials N groups in a row re-picked the identical dead
// first hop + intermediate every time. Observed live in
// bench/2026-09-16/c302e1746-smoke/mux-standby-3: rg49187, rg49188 and rg49189
// were each dialed over the same first hop and each closed at exactly 10.00s.
//
// deadRouteCache is that missing memory: a small, short-lived, router-scoped
// set of routes that died young without carrying payload. It is deliberately
// keyed on the first hop AND the rest of the path, not on the first hop alone —
// excluding a whole transport would starve a visor whose only fast uplink is
// that transport, while a different route over the same first hop is a genuinely
// different experiment.
//
// Repeat deaths back off (doubling up to deadRouteMaxTTL) so a route that keeps
// dying stops being retried every minute, and any route that does carry payload
// clears its entry immediately.
const (
	// deadRouteYoungAge is the "died right after dial" window. A group that
	// lives longer than this and then closes is a normal teardown, not a dead
	// route. Sized just above handshakeAwaitTimeout (10s) so the handshake
	// timeout itself always lands inside it.
	deadRouteYoungAge = 12 * time.Second

	// deadRouteTTL is the first exclusion window; deadRouteMaxTTL caps the
	// doubling applied on each repeat death.
	deadRouteTTL    = 60 * time.Second
	deadRouteMaxTTL = 8 * time.Minute
)

// deadRouteKey identifies one route: its first-hop transport plus a digest of
// the remaining hops.
type deadRouteKey struct {
	firstTp uuid.UUID
	rest    uint64
}

type deadRouteEntry struct {
	until   time.Time
	ttl     time.Duration
	at      time.Time     // when the death was recorded
	age     time.Duration // how long the group lived before dying
	strikes int
}

// deadRouteCache is safe for concurrent use.
type deadRouteCache struct {
	mu  sync.Mutex
	ttl time.Duration
	max time.Duration
	m   map[deadRouteKey]deadRouteEntry
}

func newDeadRouteCache(ttl, max time.Duration) *deadRouteCache {
	if ttl <= 0 {
		ttl = deadRouteTTL
	}
	if max < ttl {
		max = ttl
	}
	return &deadRouteCache{ttl: ttl, max: max, m: make(map[deadRouteKey]deadRouteEntry)}
}

// deadRouteKeyOf digests a forward path into a cache key. Reports ok=false for
// an empty path (nothing to remember).
func deadRouteKeyOf(path []routing.Hop) (deadRouteKey, bool) {
	if len(path) == 0 {
		return deadRouteKey{}, false
	}
	h := fnv.New64a()
	for _, hop := range path[1:] {
		_, _ = h.Write(hop.TpID[:])
		_, _ = h.Write(hop.To[:])
	}
	return deadRouteKey{firstTp: path[0].TpID, rest: h.Sum64()}, true
}

// mark records that the route died `age` after it was created. Deaths older
// than deadRouteYoungAge are ignored — only a route that died YOUNG is evidence.
func (c *deadRouteCache) mark(path []routing.Hop, age time.Duration, now time.Time) bool {
	if c == nil || age > deadRouteYoungAge {
		return false
	}
	k, ok := deadRouteKeyOf(path)
	if !ok {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(now)
	e := c.m[k]
	ttl := c.ttl
	if e.strikes > 0 && e.ttl > 0 {
		ttl = e.ttl * 2
	}
	if ttl > c.max {
		ttl = c.max
	}
	c.m[k] = deadRouteEntry{until: now.Add(ttl), ttl: ttl, at: now, age: age, strikes: e.strikes + 1}
	return true
}

// clear forgets a route — called when it proved itself by carrying payload.
func (c *deadRouteCache) clear(path []routing.Hop) {
	if c == nil {
		return
	}
	k, ok := deadRouteKeyOf(path)
	if !ok {
		return
	}
	c.mu.Lock()
	delete(c.m, k)
	c.mu.Unlock()
}

// excluded reports whether the route is still inside its exclusion window.
func (c *deadRouteCache) excluded(path []routing.Hop, now time.Time) (deadRouteEntry, bool) {
	if c == nil {
		return deadRouteEntry{}, false
	}
	k, ok := deadRouteKeyOf(path)
	if !ok {
		return deadRouteEntry{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok {
		return deadRouteEntry{}, false
	}
	if !now.Before(e.until) {
		delete(c.m, k)
		return deadRouteEntry{}, false
	}
	return e, true
}

func (c *deadRouteCache) pruneLocked(now time.Time) {
	for k, e := range c.m {
		if !now.Before(e.until) {
			delete(c.m, k)
		}
	}
}

// filterDeadRoutes drops candidates whose route died right after a recent dial.
// It NEVER returns an empty set: if every candidate is excluded the caller is
// better served by retrying the least-bad one than by failing the dial with no
// route at all, so the input is passed through unchanged (and said so in the
// trail).
func (r *router) filterDeadRoutes(cands [][]routing.Hop, opts *DialOptions) [][]routing.Hop {
	if r == nil || r.deadRoutes == nil || len(cands) == 0 {
		return cands
	}
	now := time.Now()
	keep := make([][]routing.Hop, 0, len(cands))
	var excluded []string
	for _, hops := range cands {
		if e, bad := r.deadRoutes.excluded(hops, now); bad {
			excluded = append(excluded, deadRouteTrail(hops, e))
			continue
		}
		keep = append(keep, hops)
	}
	if len(excluded) == 0 {
		return cands
	}
	if len(keep) == 0 {
		if len(cands) > 1 {
			opts.note("every candidate recently died young (%s); keeping them rather than failing the dial", strings.Join(excluded, ", "))
		}
		return cands
	}
	opts.note("excluded: %s", strings.Join(excluded, ", "))
	return keep
}

func deadRouteTrail(hops []routing.Hop, e deadRouteEntry) string {
	first := "none"
	if len(hops) > 0 {
		first = hops[0].TpID.String()[:8]
	}
	return fmt.Sprintf("%s died %.1fs after dial at %s (strike %d, excluded for %s)",
		first, e.age.Seconds(), e.at.UTC().Format(time.RFC3339), e.strikes, e.ttl)
}
