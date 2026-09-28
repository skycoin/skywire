// Package preset pkg/router/policy/preset/engines.go c2-net-routing
package preset

import "strconv"

// engineIdleCalls is how many Engines.For calls a route group may go without
// ticking before its controller is dropped. Every live group ticks each
// rotation interval, so a group absent this long has closed.
const engineIdleCalls = 4096

// Engines keeps one Engine per route group. An app's policy module is loaded
// once and ticked by every route group the app holds — a skysocks-client runs
// several at once — so a single shared Engine let one group's byte counters
// become another's baseline (the per-leg maps are keyed by transport_id, which
// groups share) and one group's cooldown and saturation streaks steer
// another's reshapes. Keyed per group, each controller sees only its own legs.
//
// Not safe for concurrent use: the host serializes on_tick per module.
type Engines struct {
	byGroup map[string]*groupEngine
	calls   uint64
}

type groupEngine struct {
	e        *Engine
	lastCall uint64
}

// For returns the Engine for group, creating it on first use. An empty group
// (a host that sends no route-group identity) gets one shared Engine, which is
// the behavior before per-group state existed.
func (s *Engines) For(group string) *Engine {
	if s.byGroup == nil {
		s.byGroup = make(map[string]*groupEngine)
	}
	s.calls++
	g, ok := s.byGroup[group]
	if !ok {
		g = &groupEngine{e: New()}
		s.byGroup[group] = g
	}
	g.lastCall = s.calls
	if s.calls%256 == 0 {
		for k, v := range s.byGroup {
			if s.calls-v.lastCall > engineIdleCalls {
				delete(s.byGroup, k)
			}
		}
	}
	return g.e
}

// Len reports how many route groups currently hold a controller.
func (s *Engines) Len() int { return len(s.byGroup) }

// GroupKey names a route group from its routing context: the peer, the remote
// port and the group's own local port, which together are unique per group.
// A zero local port yields "", the shared-engine key.
func GroupKey(peerPK string, port, localPort uint16) string {
	if localPort == 0 {
		return ""
	}
	return peerPK + "|" + strconv.Itoa(int(port)) + "|" + strconv.Itoa(int(localPort))
}
