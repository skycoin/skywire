package router

import (
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// growLegs extends the mux's leg records to cover n legs, without naming them.
func (m *routeMux) growLegs(n int) {
	m.syncLegs(make([]uuid.UUID, n))
}

// The index forms below credit the leg now at idx, for tests that hold no lock
// and change no legs. Production code credits a record taken under rg.mu.

func (m *routeMux) recordSent(idx int, n uint64)    { m.legAt(idx).recordSent(n) }
func (m *routeMux) recordRecv(idx int, n uint64)    { m.legAt(idx).recordRecv(n) }
func (m *routeMux) recordPayload(idx int, n uint64) { m.legAt(idx).recordPayload(n) }
func (m *routeMux) recordRetransmit(idx int)        { m.legAt(idx).recordRetransmit() }

// legFlags returns each leg's ready and standby flag, in leg order.
func (m *routeMux) legFlags() (ready, standby []bool) {
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	for _, l := range m.legs {
		ready = append(ready, l.ready)
		standby = append(standby, l.standby)
	}
	return ready, standby
}

// legGroupIDs returns each leg's recorded group id in leg order, nil when no
// leg has one.
func (m *routeMux) legGroupIDs() []int {
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	var out []int
	for i, l := range m.legs {
		if l.group != noLegGroup && out == nil {
			out = make([]int, len(m.legs))
			for j := range out[:i] {
				out[j] = noLegGroup
			}
		}
		if out != nil {
			out[i] = l.group
		}
	}
	return out
}

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
