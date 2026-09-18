//go:build !tinygo || (js && wasm)

// Package router pkg/router/setup_path_stats.go c2-net-routing
//
// Which setup path this visor's routes actually took, on the VISOR side.
//
// The setup node's /stats counts requests by kind, but a visor operator cannot
// read the setup node's whitelisted view and, more to the point, the question
// they are asking is about their own dials: did the batched form get used at
// all, did the oracle query once per fill or once per route, and did the
// concurrent dials of a fill end up on distinct intermediates. This is that,
// under `visor state --select diag` as route_setup.
package router

// SetupPathStats is the visor-side companion to the setup node's
// requests_by_kind: how this visor's own route setups were issued.
type SetupPathStats struct {
	// Batched is how many routes left inside a batched setup request.
	Batched uint64 `json:"batched"`
	// Singles is how many left on their own because no sibling dial to the
	// same exit was in the collection window.
	Singles uint64 `json:"singles"`
	// Fallback is how many fell back to a single request because the setup
	// node did not advertise batch-route-setup.
	Fallback uint64 `json:"fallback"`

	// OracleQueries is how many signed destination-transport queries were
	// actually issued, OracleShared how many dials rode one another's query,
	// and OracleHits how many read a cached candidate set outright. A fill of
	// N tunnels should show ONE query and N-1 shared/hit.
	OracleQueries uint64 `json:"oracle_queries"`
	OracleShared  uint64 `json:"oracle_shared"`
	OracleHits    uint64 `json:"oracle_hits"`
	// OracleClaimed is how many dials were handed a candidate no sibling held;
	// OracleCrowded how many found every candidate claimed and fell back to the
	// best one. A healthy fill is all claimed.
	OracleClaimed uint64 `json:"oracle_claimed"`
	OracleCrowded uint64 `json:"oracle_crowded"`
	// OracleExits / OracleLegs are the cache's current size: exits with a live
	// candidate set, and total candidates across them.
	OracleExits int `json:"oracle_exits"`
	OracleLegs  int `json:"oracle_legs"`

	// WarmHits / WarmMisses / WarmStored are the shared plan pool's counters —
	// the aux-leg side of the same planning work.
	WarmHits   uint64 `json:"warm_hits"`
	WarmMisses uint64 `json:"warm_misses"`
	WarmStored uint64 `json:"warm_stored"`
}

// SetupPathStats reports how this visor's route setups were issued. Safe on a
// partially initialized router: an absent subsystem contributes zeroes.
func (r *router) SetupPathStats() SetupPathStats {
	var s SetupPathStats
	if d, ok := r.conf.RouteGroupDialer.(*setupNodeDialer); ok && d != nil {
		b := d.batcher.stats()
		s.Batched, s.Singles, s.Fallback = b.Batched, b.Singles, b.Fallback
	}
	o := r.oraclePlans.stats()
	s.OracleQueries, s.OracleShared, s.OracleHits = o.Queries, o.Shared, o.Hits
	s.OracleClaimed, s.OracleCrowded = o.Claimed, o.Crowded
	s.OracleExits, s.OracleLegs = o.Exits, o.Legs
	w := r.warmRoutes.stats()
	s.WarmHits, s.WarmMisses, s.WarmStored = w.Hits, w.Misses, w.Stored
	return s
}
