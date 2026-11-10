package router

import (
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// testLegs builds a leg slice from parallel lists, one leg per transport. A
// rule list shorter than tps leaves the missing rules nil.
func testLegs(tps []*transport.ManagedTransport, fwd, rvs []routing.Rule) []groupLeg {
	legs := make([]groupLeg, len(tps))
	for i, tp := range tps {
		legs[i].tp = tp
		if i < len(fwd) {
			legs[i].fwd = fwd[i]
		}
		if i < len(rvs) {
			legs[i].rvs = rvs[i]
		}
	}
	return legs
}

// tpLegs builds legs that carry a transport and no rules.
func tpLegs(tps []*transport.ManagedTransport) []groupLeg {
	return testLegs(tps, nil, nil)
}

// setTestLegs installs rg's legs from parallel lists. Consume rules past the
// last transport become reverse-only rules. Caller holds rg.mu if it matters.
func setTestLegs(rg *RouteGroup, tps []*transport.ManagedTransport, fwd, rvs []routing.Rule) {
	rg.legs = testLegs(tps, fwd, rvs)
	rg.rvsOnly = nil
	if len(rvs) > len(tps) {
		rg.rvsOnly = append([]routing.Rule(nil), rvs[len(tps):]...)
	}
}

// testFwd lists the forward rules rg's legs hold, in leg order.
func testFwd(rg *RouteGroup) []routing.Rule {
	var out []routing.Rule
	for _, lg := range rg.legs {
		if lg.fwd != nil {
			out = append(out, lg.fwd)
		}
	}
	return out
}

// testRvs lists rg's consume rules: one slot per leg, nil on a forward-only
// leg, then the reverse-only rules.
func testRvs(rg *RouteGroup) []routing.Rule {
	out := make([]routing.Rule, 0, len(rg.legs)+len(rg.rvsOnly))
	for _, lg := range rg.legs {
		out = append(out, lg.rvs)
	}
	return append(out, rg.rvsOnly...)
}
