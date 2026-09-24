//go:build !tinygo || (js && wasm)

// Package router pkg/router/leg_hops_match.go c2-net-routing
//
// One hop length per packet-level group (leg.hops_match).
//
// A packet-level group stripes every frame across its legs, so its legs should
// be interchangeable: a direct transport to the exit next to a two-hop chain
// reorders against it on every frame, fails over to a path with a different
// shape, and hands the direct leg's observer the exit relationship the chain
// was built to hide. Every path that adds a leg on its own — the pool arbiter,
// the shape converger, the pool plan, the route-finder grow, the dial-time and
// rotation aux legs — asks legHopsMatch first. An operator's explicit
// `mux add` is not gated.
package router

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
)

// legHopsTarget is the forward hop count every automatically added leg of rg
// must have: the length of the group's first live leg. 0 means no constraint —
// the knob is off, or the first leg's route was never recorded.
func (rg *RouteGroup) legHopsTarget() int {
	if rg == nil || !rg.knBool(routersettings.LegHopsMatch) {
		return 0
	}
	first := rg.firstLegTpID()
	if first == (uuid.UUID{}) {
		return 0
	}
	return len(rg.legHopsFor(first))
}

// legHopsMatch reports whether a leg with forward route fwd may join rg under
// leg.hops_match. An unknown route (nil fwd) never matches a known target.
func (rg *RouteGroup) legHopsMatch(fwd []routing.Hop) bool {
	target := rg.legHopsTarget()
	return target == 0 || len(fwd) == target
}

// errNoLegGrown is a grow that returned without error but added nothing — no
// plan, no route, no other direct transport. It is not a take.
var errNoLegGrown = errors.New("no leg grown")

// growAtHopLength is the arbiter's take when no pooled chain has g's hop
// length: dial a leg of that length instead (another direct transport for a
// direct group, a route-finder chain of the same length otherwise). A try is
// paced by pool.leg_interval whether or not it lands, so a group with no such
// leg to be had asks once per interval, not once per tick.
func (rg *RouteGroup) growAtHopLength(now time.Time, reason string, grow func(*RouteGroup) error) {
	hops := rg.legHopsTarget()
	if grow == nil || hops == 0 {
		return
	}
	rg.mu.Lock()
	rg.poolLastTake = now
	rg.mu.Unlock()
	if err := grow(nil); err != nil {
		rg.logger.WithError(err).Debugf("Pool arbiter: no pooled chain of %d hop(s) and none could be dialed", hops)
		return
	}
	rg.notePoolLegTaken(poolCandidate{}, now, reason,
		fmt.Sprintf("dialed at %d hop(s); no pooled chain of that length (leg.hops_match)", hops))
}

// pooledAtOtherLength reports whether the pool holds a chain g passed over only
// for its length. The fallback dial is for that case alone: an empty pool, or
// one whose chains all share a held first hop, settles as it always has.
func pooledAtOtherLength(g *RouteGroup, pool []*RouteGroup) bool {
	target := g.legHopsTarget()
	if target == 0 {
		return false
	}
	for _, s := range pool {
		if s == nil || s == g || s.isClosed() || !s.standbyTunnel() {
			continue
		}
		if first := s.firstLegTpID(); first != (uuid.UUID{}) && len(s.legHopsFor(first)) != target {
			return true
		}
	}
	return false
}
