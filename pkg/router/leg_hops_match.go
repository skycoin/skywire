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
