// Package router pkg/router/suspect_hops.go c2-net-routing
package router

import (
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// defaultSuspectHopTTL is how long an intermediate visor stays on the
// per-destination failed-hop exclusion list after a route through it failed to
// complete its setup handshake within handshakeAwaitTimeout.
//
// This is the "self-heal" half of the dead-edge route-setup fix (#80 route-
// level failover follow-up to #4057). #4057's handshake retransmit recovers a
// single lost setup packet on an otherwise-live path; it does NOT help when the
// picked path funnels through an intermediary that ACKs route-setup but does not
// forward data — every retransmit is futile, the whole handshake window is
// burned, and the app's reconnect loop re-runs the route-finder which (ranking
// by latency, not liveness) hands back the SAME dead-hop candidate set. Marking
// the failed intermediary suspect for a bounded window lets the very next dial
// exclude it up front and pick a different route.
//
// The TTL bounds the blacklisting so a hop that was merely busy/restarting is
// retried once it recovers (self-heal) — the exclusion is never permanent. 90s
// comfortably outlives the app reconnect cadence (so the reconnect after a
// wedge avoids the dead hop) while being short enough that a recovered hop
// rejoins the candidate set quickly.
var defaultSuspectHopTTL = 90 * time.Second

// resolveFailedHopExclusionTTL maps a Config.FailedHopExclusionTTL value to the
// cache TTL: 0 → the built-in default (feature on), <0 → disabled, >0 → as-is.
func resolveFailedHopExclusionTTL(cfg time.Duration) time.Duration {
	if cfg == 0 {
		return defaultSuspectHopTTL
	}
	return cfg
}

// suspectHopCache is a TTL set of intermediate-visor PKs that recently failed
// route-group setup, keyed by destination. It is consulted at the start of a
// dial to pre-seed opts.ExcludeIntermediatePKs (so a fresh reconnect avoids a
// known-dead hop) and written when a route's setup handshake fails to complete
// within handshakeAwaitTimeout.
//
// All methods are safe to call on a nil receiver (they no-op / return nil), so
// router values constructed without a cache — e.g. in narrow unit tests — keep
// their pre-existing behavior.
type suspectHopCache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[cipher.PubKey]map[cipher.PubKey]time.Time // dst -> intermediate -> expiry
}

// newSuspectHopCache builds a cache with the given TTL. A non-positive TTL
// disables the cross-dial persistence entirely (mark/suspects become no-ops):
// bounded grace still applies WITHIN a single DialRoutes call via the local
// opts.ExcludeIntermediatePKs accumulation, but nothing survives to the next
// dial — the pre-fix behavior.
func newSuspectHopCache(ttl time.Duration) *suspectHopCache {
	return &suspectHopCache{
		ttl: ttl,
		m:   make(map[cipher.PubKey]map[cipher.PubKey]time.Time),
	}
}

// mark records each intermediate in inter as suspect for dst, expiring after
// the cache TTL. No-op when disabled (nil / non-positive TTL) or inter empty.
func (c *suspectHopCache) mark(dst cipher.PubKey, inter []cipher.PubKey) {
	if c == nil || c.ttl <= 0 || len(inter) == 0 {
		return
	}
	exp := time.Now().Add(c.ttl)
	c.mu.Lock()
	defer c.mu.Unlock()
	set := c.m[dst]
	if set == nil {
		set = make(map[cipher.PubKey]time.Time, len(inter))
		c.m[dst] = set
	}
	for _, pk := range inter {
		if pk.Null() {
			continue
		}
		set[pk] = exp
	}
}

// suspects returns the non-expired suspect intermediates for dst, pruning
// expired entries as it goes (the self-heal step — a hop past its TTL rejoins
// the candidate set). Returns nil when disabled or nothing is currently
// suspect.
func (c *suspectHopCache) suspects(dst cipher.PubKey) []cipher.PubKey {
	if c == nil || c.ttl <= 0 {
		return nil
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	set := c.m[dst]
	if len(set) == 0 {
		return nil
	}
	out := make([]cipher.PubKey, 0, len(set))
	for pk, exp := range set {
		if now.After(exp) {
			delete(set, pk)
			continue
		}
		out = append(out, pk)
	}
	if len(set) == 0 {
		delete(c.m, dst)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
