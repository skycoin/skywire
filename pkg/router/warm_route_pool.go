// pkg/router/warm_route_pool.go — a VISOR-LEVEL shared cache of disjoint route
// PLANS to an exit, shared by every route group that dials aux mux legs to that
// exit. See docs/design/shared-warm-route-pool.md for the full design.
//
// Motivation: today each mux-enabled route group re-discovers its own disjoint
// aux-leg set. establishMuxRoutes and addOneAuxLeg both call fetchBestRoutes
// (the route-finder round-trip + the local disjointness BFS/validMuxLeg gate)
// once PER LEG, PER GROUP. With N tunnels aggregating to the same exit that is
// N× the same route-finder work — the "planning storm" half of the cost the
// user flagged (the setup-node DIAL half is intrinsic to per-group rules and is
// NOT removed by this cache; see the RFC).
//
// What is safely shareable: a "plan" is a forward+reverse []routing.Hop pair —
// PATH ONLY, no route IDs (routing.Hop is {From, To, TpID}). Route IDs are
// reserved fresh per dial by the IDReserver, and the intermediate rules that
// carry them are bound to one route group's descriptor; those CANNOT be shared
// (see the RFC's layer analysis). The path itself is group-independent: any
// group dialing an aux leg to the same exit can feed a cached plan straight into
// its own setup-node Dial, which mints that group's own route-ID chain over the
// (already-shared) transports.
//
// Safety: a cached plan is only ever a HINT. Every caller runs the exact same
// validMuxLeg gate on the returned plan that it already runs on a freshly
// fetched one, and the setup-node Dial re-resolves/re-dials the first-hop
// transport — so a stale plan (a transport that has since died) is rejected or
// fails the dial and the caller falls through to a fresh fetchBestRoutes, i.e.
// exactly today's behavior. A miss is always a clean fallback: this cache can
// only save work, never change the leg that gets built.
//
// This is purely a local read-side cache. It is initiator-local — no wire
// protocol change — so unlike the mux data-plane features it needs no
// capability negotiation to be safe (a peer is unaffected by how the initiator
// planned the path). Phase 2/3 (a genuinely shared warm-TRANSPORT pin, and
// eventually shared route-ID trunks) DO touch the wire and would follow the
// handshake-capability pattern — see the RFC.

// Package router pkg/router/warm_route_pool.go c2-net-routing
package router

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// defaultWarmPlanTTL bounds staleness of a cached disjoint plan set. Plans are
// paths over the transport graph, which only changes on transport
// register/deregister; a short TTL keeps a genuinely-removed transport from
// lingering in a served plan while still coalescing the burst of per-leg
// planning that N tunnels to one exit produce in the same few seconds. Kept
// well under the tpdSnapshotCache 5m TTL the underlying fetch already uses,
// since a plan is more perishable than the raw TPD dataset.
const defaultWarmPlanTTL = 30 * time.Second

// warmPlanBucketCap bounds how many distinct disjoint plans are held per exit.
// A group growing its k-th aux leg needs a cached plan disjoint from its own
// k-1 prior intermediates, so the bucket must hold several; but it is a cache,
// not the authority on the topology's disjoint count, so it is capped to keep
// memory bounded on a visor tunneling to many exits.
const warmPlanBucketCap = 64

// routePlan is one cached, group-INDEPENDENT path to an exit: a forward +
// reverse hop sequence (no route IDs) plus the derived keys used to match it
// against a caller's per-group exclude set.
type routePlan struct {
	fwd     []routing.Hop
	rev     []routing.Hop
	firstTp uuid.UUID       // fwd[0].TpID — the first-hop transport this plan rides
	inter   []cipher.PubKey // intermediate visor PKs (for disjointness matching)
}

// planKey identifies a bucket of disjoint plans to one exit at one min-hops
// class. min-hops is part of the key because a min_hops>=2 dial must not be
// served a 1-hop direct plan cached for a min_hops=1 dial.
type planKey struct {
	dst     cipher.PubKey
	minHops uint16
}

type planBucket struct {
	plans  []routePlan
	filled time.Time
}

// warmRoutePool is the visor-level shared plan cache. Concurrent-safe. One
// instance is held by the router and consulted by every group's aux-leg dial.
type warmRoutePool struct {
	mu     sync.Mutex
	ttl    time.Duration
	now    func() time.Time
	byExit map[planKey]*planBucket

	// metrics (read via stats() for observability; never gate behavior)
	hits   uint64
	misses uint64
	stored uint64
}

func newWarmRoutePool(ttl time.Duration) *warmRoutePool {
	if ttl <= 0 {
		ttl = defaultWarmPlanTTL
	}
	return &warmRoutePool{
		ttl:    ttl,
		now:    time.Now,
		byExit: make(map[planKey]*planBucket),
	}
}

// planIntermediates extracts intermediate visor PKs from a forward hop
// sequence (mirrors intermediatesOfHops but on a bare []Hop, src/dst derived
// from the hops themselves so the pool needs no descriptor).
func planIntermediates(fwd []routing.Hop) []cipher.PubKey {
	if len(fwd) <= 1 {
		return nil
	}
	src := fwd[0].From
	dst := fwd[len(fwd)-1].To
	out := make([]cipher.PubKey, 0, len(fwd)-1)
	for i := 0; i < len(fwd)-1; i++ {
		pk := fwd[i].To
		if pk == src || pk == dst {
			continue
		}
		out = append(out, pk)
	}
	return out
}

// put records a freshly-discovered plan for (dst, minHops). Called AFTER a
// caller's own fetchBestRoutes succeeds, so the cache is populated as a
// side-effect of the first group that plans a leg to this exit and then serves
// every later group/leg. Deduplicates by first-hop transport (two plans with
// the same first hop are, for mux purposes, the same leg). Nil/empty forward
// plans are ignored.
func (p *warmRoutePool) put(dst cipher.PubKey, minHops uint16, fwd, rev []routing.Hop) {
	if p == nil || len(fwd) == 0 {
		return
	}
	key := planKey{dst: dst, minHops: minHops}
	plan := routePlan{
		fwd:     append([]routing.Hop(nil), fwd...),
		rev:     append([]routing.Hop(nil), rev...),
		firstTp: fwd[0].TpID,
		inter:   planIntermediates(fwd),
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	b := p.byExit[key]
	now := p.now()
	if b == nil || now.Sub(b.filled) > p.ttl {
		// Fresh bucket (new exit, or the old one expired): start clean so a
		// stale plan can't outlive its TTL by being appended alongside fresh
		// ones under the same filled stamp.
		b = &planBucket{plans: make([]routePlan, 0, 4)}
		p.byExit[key] = b
	}
	for i := range b.plans {
		if b.plans[i].firstTp == plan.firstTp {
			b.plans[i] = plan // refresh in place
			b.filled = now
			return
		}
	}
	if len(b.plans) >= warmPlanBucketCap {
		b.plans = b.plans[1:] // drop oldest, bounded memory
	}
	b.plans = append(b.plans, plan)
	b.filled = now
	p.stored++
}

// disjointFrom reports whether plan's first-hop transport and intermediates
// avoid the caller's per-group exclude sets — the same disjointness the caller
// would demand of a freshly-fetched leg.
func (pl *routePlan) disjointFrom(excludeTps map[uuid.UUID]struct{}, excludePKs map[cipher.PubKey]struct{}) bool {
	if _, bad := excludeTps[pl.firstTp]; bad {
		return false
	}
	for _, pk := range pl.inter {
		if _, bad := excludePKs[pk]; bad {
			return false
		}
	}
	return true
}

// bestPlan returns a cached, non-stale plan to (dst, minHops) whose first-hop
// transport and intermediates are disjoint from the caller's exclude sets, or
// ok=false on a miss. The returned slices are the cached copies — callers feed
// them into a setup-node Dial (read-only) and run their usual validMuxLeg gate,
// so aliasing is safe. A miss is a clean signal to fall back to fetchBestRoutes.
func (p *warmRoutePool) bestPlan(dst cipher.PubKey, minHops uint16, excludeTps []uuid.UUID, excludePKs []cipher.PubKey) (fwd, rev []routing.Hop, ok bool) {
	if p == nil {
		return nil, nil, false
	}
	key := planKey{dst: dst, minHops: minHops}
	exTp := make(map[uuid.UUID]struct{}, len(excludeTps))
	for _, id := range excludeTps {
		exTp[id] = struct{}{}
	}
	exPK := make(map[cipher.PubKey]struct{}, len(excludePKs))
	for _, pk := range excludePKs {
		exPK[pk] = struct{}{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	b := p.byExit[key]
	if b == nil || p.now().Sub(b.filled) > p.ttl {
		if b != nil {
			delete(p.byExit, key) // evict expired bucket
		}
		p.misses++
		return nil, nil, false
	}
	for i := range b.plans {
		if b.plans[i].disjointFrom(exTp, exPK) {
			p.hits++
			return b.plans[i].fwd, b.plans[i].rev, true
		}
	}
	p.misses++
	return nil, nil, false
}

// invalidate drops all cached plans to one exit — called when a transport to a
// relevant peer comes or goes (a register/deregister may open or close a
// disjoint path). Cheap; the next dial re-fills from a fresh fetch.
func (p *warmRoutePool) invalidate(dst cipher.PubKey) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.byExit {
		if k.dst == dst {
			delete(p.byExit, k)
		}
	}
}

// invalidateAll drops every cached plan (e.g. a bulk transport-set change).
func (p *warmRoutePool) invalidateAll() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.byExit = make(map[planKey]*planBucket)
}

// warmPoolStats is a point-in-time snapshot for observability.
type warmPoolStats struct {
	Exits  int
	Plans  int
	Hits   uint64
	Misses uint64
	Stored uint64
}

func (p *warmRoutePool) stats() warmPoolStats {
	if p == nil {
		return warmPoolStats{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	s := warmPoolStats{Hits: p.hits, Misses: p.misses, Stored: p.stored}
	s.Exits = len(p.byExit)
	for _, b := range p.byExit {
		s.Plans += len(b.plans)
	}
	return s
}
