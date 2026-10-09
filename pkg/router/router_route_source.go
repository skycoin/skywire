// Package router pkg/router/router_route_source.go c3-rtr-core
//
// Where multi-hop routes came from, for `visor state --select diag`: the
// route finder, the local graph (a hypervisor's attached visors, #4750), or
// the local fallback after a route-finder miss. Counted so the saving from a
// local graph is visible in a snapshot rather than only in logs.
package router

import "sync/atomic"

// RouteSourceStats counts fetchBestRoutes outcomes since the router started.
type RouteSourceStats struct {
	// Requests is fetchBestRoutes calls; DirectHops is how many were served
	// by a transport this visor already holds.
	Requests   int64 `json:"requests"`
	DirectHops int64 `json:"direct_hops"`
	// OracleAttempts is 2-hop calculations from the destination's own list
	// through the setup node; OracleRoutes is how many found a route.
	OracleAttempts int64 `json:"oracle_attempts"`
	OracleRoutes   int64 `json:"oracle_routes"`
	// LocalAttached is routes built from the local graph before the route
	// finder was asked (Config.PreferLocalRouteTo): each one is a route-finder
	// query that did not happen.
	LocalAttached int64 `json:"local_attached"`
	// RouteFinderQueries is route-finder requests made (one per attempt).
	RouteFinderQueries int64 `json:"route_finder_queries"`
	// LocalFallback is routes built locally after the route finder missed or
	// ran out of retries.
	LocalFallback int64 `json:"local_fallback"`
	// DirectSetups is one-hop routes set up with the peer, without a setup node.
	DirectSetups int64 `json:"direct_setups"`
	// DirectSetupFallbacks is one-hop routes the peer could not set up
	// directly, sent to a setup node instead.
	DirectSetupFallbacks int64 `json:"direct_setup_fallbacks"`
	// ListAttempts is 3-hop calculations from other visors' own transport
	// lists; ListRoutes is how many found a route. The ListNo* counters say
	// why the rest did not: no usable first hop, no list from the destination,
	// no list from any neighbor, or no path through the lists that came back.
	ListAttempts       int64 `json:"list_attempts"`
	ListRoutes         int64 `json:"list_routes"`
	ListNoFirstHop     int64 `json:"list_no_first_hop"`
	ListNoDstList      int64 `json:"list_no_dst_list"`
	ListNoNeighborList int64 `json:"list_no_neighbor_list"`
	ListNoPath         int64 `json:"list_no_path"`
	// ListFetches is neighbor lists asked for; ListFetchFails is how many
	// did not come back.
	ListFetches    int64 `json:"list_fetches"`
	ListFetchFails int64 `json:"list_fetch_fails"`
}

type routeSourceCounters struct {
	requests       atomic.Int64
	directHops     atomic.Int64
	oracleAttempts atomic.Int64
	oracleRoutes   atomic.Int64

	localAttached atomic.Int64
	rfQueries     atomic.Int64
	localFallback atomic.Int64

	directSetups         atomic.Int64
	directSetupFallbacks atomic.Int64

	listAttempts       atomic.Int64
	listRoutes         atomic.Int64
	listNoFirstHop     atomic.Int64
	listNoDstList      atomic.Int64
	listNoNeighborList atomic.Int64
	listNoPath         atomic.Int64
	listFetches        atomic.Int64
	listFetchFails     atomic.Int64
}

// RouteSourceStats returns the counters.
func (r *router) RouteSourceStats() RouteSourceStats {
	c := &r.routeSource
	return RouteSourceStats{
		Requests:             c.requests.Load(),
		DirectHops:           c.directHops.Load(),
		OracleAttempts:       c.oracleAttempts.Load(),
		OracleRoutes:         c.oracleRoutes.Load(),
		LocalAttached:        c.localAttached.Load(),
		RouteFinderQueries:   c.rfQueries.Load(),
		LocalFallback:        c.localFallback.Load(),
		DirectSetups:         c.directSetups.Load(),
		DirectSetupFallbacks: c.directSetupFallbacks.Load(),
		ListAttempts:         c.listAttempts.Load(),
		ListRoutes:           c.listRoutes.Load(),
		ListNoFirstHop:       c.listNoFirstHop.Load(),
		ListNoDstList:        c.listNoDstList.Load(),
		ListNoNeighborList:   c.listNoNeighborList.Load(),
		ListNoPath:           c.listNoPath.Load(),
		ListFetches:          c.listFetches.Load(),
		ListFetchFails:       c.listFetchFails.Load(),
	}
}
