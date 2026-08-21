// Package router pkg/router/parallel_route_config.go c2-net-routing
//
// This file holds the pieces of the parallel-route-setup feature that must
// compile for EVERY build target (including the plain-tinygo edge whose
// router.go still calls newSuspectHopCache and whose SetDefaults reads the
// K bounds): the config bounds and the minimal suspect-hop penalty cache. The
// race machinery itself (which references the route-setup dialer, NoiseRouteGroup,
// route-finder, etc.) lives in parallel_route_setup.go behind the non-tinygo
// build tag. Keep this file free of route-setup-only symbols.
package router

import (
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

const (
	// defaultParallelRouteSetup is the number of candidate route groups
	// DialRoutes races per attempt when routing.parallel_route_setup is
	// unset. Small on purpose: a few concurrent reservations are enough to
	// route around the odd dead intermediate without fanning the setup load
	// out across the whole candidate set.
	defaultParallelRouteSetup = 3
	// maxParallelRouteSetup bounds the fan-out so a misconfigured value can't
	// flood the route-setup control plane with concurrent reservations.
	maxParallelRouteSetup = 5
)

// suspectHopCache is a lightweight, self-contained penalty cache: an
// intermediate PK that just lost a route-setup race — or failed setup on the
// handshake-timeout OR ctx-deadline / setup-canceled path — is marked
// "suspect" for a short TTL so the NEXT dial deprioritizes routes through it,
// front-loading known-good hops.
//
// It is intentionally minimal and GLOBAL (per-intermediate, TTL-bounded).
// Companion PR #4063 (branch fix/router-suspect-hop-failover, file
// pkg/router/suspect_hops.go) adds a richer PER-DESTINATION TTL suspect cache
// keyed off handshakeAwaitTimeout. This cache is structured to compose with —
// or be superseded by — that one: the router holds a single `suspects` field,
// and the arm points (raceCandidateSetup's onLoser, and DialRoutes' failure
// paths incl. ctx-deadline) are the only call sites. To reconcile with #4063,
// point those call sites at its cache (add the destination PK) and delete this
// cache. Arming on the ctx-deadline path is deliberate: that is the path #4063
// alone did not fire on live, which is why a dead hop kept being re-picked.
type suspectHopCache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[cipher.PubKey]time.Time // intermediate PK -> penalized-until
}

func newSuspectHopCache(ttl time.Duration) *suspectHopCache {
	if ttl <= 0 {
		ttl = handshakeAwaitTimeout
	}
	return &suspectHopCache{ttl: ttl, m: make(map[cipher.PubKey]time.Time)}
}

// arm marks a single intermediate PK suspect until now+ttl.
func (c *suspectHopCache) arm(pk cipher.PubKey) {
	if c == nil || pk.Null() {
		return
	}
	c.mu.Lock()
	c.m[pk] = time.Now().Add(c.ttl)
	c.mu.Unlock()
}

// armAll marks every PK in pks suspect. No-op on nil/empty.
func (c *suspectHopCache) armAll(pks []cipher.PubKey) {
	if c == nil || len(pks) == 0 {
		return
	}
	until := time.Now().Add(c.ttl)
	c.mu.Lock()
	for _, pk := range pks {
		if !pk.Null() {
			c.m[pk] = until
		}
	}
	c.mu.Unlock()
}

// isSuspect reports whether pk is currently penalized (and lazily evicts it
// once its TTL has passed so the map does not grow unbounded).
func (c *suspectHopCache) isSuspect(pk cipher.PubKey, now time.Time) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	until, ok := c.m[pk]
	if !ok {
		return false
	}
	if now.After(until) {
		delete(c.m, pk)
		return false
	}
	return true
}
