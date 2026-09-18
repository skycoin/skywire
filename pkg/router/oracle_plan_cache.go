//go:build !tinygo || (js && wasm)

// Package router pkg/router/oracle_plan_cache.go c2-net-routing
//
// ONE destination-transport query per fill, and N distinct paths out of it.
//
// The RSN-oracle already computes the whole candidate set: rsn_oracle_routes.go
// intersects this visor's own live transports with the destination's own
// transports (fetched authoritatively through an RSN-signed query) and ranks
// every shared intermediate. On the campaign rig that intersection is 274
// common intermediates — the local visor holds transports to 349 peers, the
// exit to 576 — and computeDisjoint2HopRoutes returns all 274, best first.
//
// Then oracle2HopRoutes threw 273 of them away and returned legs[0], because
// its caller wanted one route. So a standby-pool fill of eight tunnels paid
// eight signed transport queries (a setup-node round trip plus a dmsg-direct
// delivery to the exit, capped at 14 s each) to compute the same 274-element
// set eight times — and, since a dial's exclusions come from SIBLING route
// groups that do not exist yet when the dials are concurrent, every one of them
// would have ranked the same intermediate first.
//
// This cache fixes both halves:
//
//   - legsFor is a singleflight with a TTL (warm.plan_ttl). N concurrent dials
//     to one exit make ONE query; the rest wait on it and read the same set.
//   - claim hands each caller the best candidate no sibling has taken, holding
//     that claim for setup.plan_claim_ttl. N concurrent dials therefore take N
//     DISTINCT intermediates without needing to see each other's route groups.
//
// A claim is advisory. It expires on its own, it is never a promise that the
// dial succeeded, and a caller that finds every candidate claimed falls back to
// the best one — the previous behavior, never worse.
package router

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// oraclePlanSet is one exit's ranked candidate set plus the claims against it.
type oraclePlanSet struct {
	legs   []twoHopLeg
	filled time.Time
	// claims maps a leg's FIRST-HOP transport id to when it was claimed. The
	// first hop is what must differ between sibling tunnels (a shared first hop
	// is a shared bottleneck), and it is unique per leg because
	// computeDisjoint2HopRoutes emits one leg per intermediate.
	claims map[uuid.UUID]time.Time
}

// oraclePlanCache is the visor-level singleflight + claim ledger over the
// RSN-oracle's candidate sets. Concurrent-safe; one instance per router.
type oraclePlanCache struct {
	mu       sync.Mutex
	sets     map[batchKey]*oraclePlanSet
	inflight map[batchKey]chan struct{}
	now      func() time.Time

	// metrics (read via stats(); never gate behavior)
	queries uint64 // oracle queries actually issued
	shared  uint64 // queries a caller waited on instead of issuing
	hits    uint64 // served from a fresh cached set
	claimed uint64 // legs handed out under a claim
	crowded uint64 // callers that found every candidate claimed
}

func newOraclePlanCache() *oraclePlanCache {
	return &oraclePlanCache{
		sets:     make(map[batchKey]*oraclePlanSet),
		inflight: make(map[batchKey]chan struct{}),
		now:      time.Now,
	}
}

// legsFor returns the ranked candidate set for (src, dst), issuing at most ONE
// fetch across all concurrent callers and reusing it for warm.plan_ttl.
//
// fetch is the real work: the RSN-signed transport query to the destination
// plus the local transport snapshot plus computeDisjoint2HopRoutes. It is
// called with the ctx of whichever caller happened to arrive first; the others
// wait on that call rather than issuing their own, and a waiter whose own ctx
// expires first returns that error without disturbing the fetch.
func (c *oraclePlanCache) legsFor(ctx context.Context, src, dst cipher.PubKey, fetch func(context.Context) ([]twoHopLeg, error)) ([]twoHopLeg, error) {
	if c == nil {
		return fetch(ctx)
	}
	key := batchKey{src: src, dst: dst}
	ttl := WarmPlanTTL()

	// waited records that this caller already rode someone else's fetch, so the
	// set it reads on the next turn of the loop is counted once — as `shared`,
	// not also as a cache `hit`.
	waited := false
	for {
		c.mu.Lock()
		if s := c.sets[key]; s != nil && c.now().Sub(s.filled) <= ttl {
			if !waited {
				c.hits++
			}
			legs := s.legs
			c.mu.Unlock()
			return legs, nil
		}
		if wait, busy := c.inflight[key]; busy {
			c.shared++
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-wait:
				waited = true
				continue // the fetch finished; re-read the set (or retry on its failure)
			}
		}
		done := make(chan struct{})
		c.inflight[key] = done
		c.queries++
		c.mu.Unlock()

		legs, err := fetch(ctx)

		c.mu.Lock()
		delete(c.inflight, key)
		if err == nil {
			c.sets[key] = &oraclePlanSet{
				legs:   legs,
				filled: c.now(),
				claims: make(map[uuid.UUID]time.Time, len(legs)),
			}
		}
		c.mu.Unlock()
		close(done)
		return legs, err
	}
}

// claim hands back the best candidate from legs that no concurrent sibling
// holds, marking it claimed for setup.plan_claim_ttl. eligible filters the
// caller's own exclusions (an excluded first hop, a dead route) and may be nil.
//
// ok=false means every eligible candidate is currently claimed; the caller
// should use its own best pick, which is what it did before claims existed.
func (c *oraclePlanCache) claim(src, dst cipher.PubKey, legs []twoHopLeg, eligible func(twoHopLeg) bool) (twoHopLeg, bool) {
	if c == nil || len(legs) == 0 {
		return twoHopLeg{}, false
	}
	key := batchKey{src: src, dst: dst}
	ttl := SetupPlanClaimTTL()

	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.sets[key]
	if s == nil {
		s = &oraclePlanSet{legs: legs, filled: c.now(), claims: make(map[uuid.UUID]time.Time, len(legs))}
		c.sets[key] = s
	}
	now := c.now()
	for _, leg := range legs {
		if len(leg.Forward) == 0 {
			continue
		}
		if eligible != nil && !eligible(leg) {
			continue
		}
		id := leg.Forward[0].TpID
		if at, held := s.claims[id]; held && now.Sub(at) <= ttl {
			continue
		}
		s.claims[id] = now
		c.claimed++
		return leg, true
	}
	c.crowded++
	return twoHopLeg{}, false
}

// release drops a claim early — a dial that failed before using its path hands
// the intermediate straight back instead of holding it for the full TTL.
func (c *oraclePlanCache) release(src, dst cipher.PubKey, leg twoHopLeg) {
	if c == nil || len(leg.Forward) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.sets[batchKey{src: src, dst: dst}]; s != nil {
		delete(s.claims, leg.Forward[0].TpID)
	}
}

// oraclePlanStats is a point-in-time snapshot for observability.
type oraclePlanStats struct {
	Exits   int    `json:"exits"`
	Legs    int    `json:"legs"`
	Queries uint64 `json:"queries"`
	Shared  uint64 `json:"shared"`
	Hits    uint64 `json:"hits"`
	Claimed uint64 `json:"claimed"`
	Crowded uint64 `json:"crowded"`
}

func (c *oraclePlanCache) stats() oraclePlanStats {
	if c == nil {
		return oraclePlanStats{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := oraclePlanStats{
		Queries: c.queries,
		Shared:  c.shared,
		Hits:    c.hits,
		Claimed: c.claimed,
		Crowded: c.crowded,
		Exits:   len(c.sets),
	}
	for _, set := range c.sets {
		s.Legs += len(set.legs)
	}
	return s
}
