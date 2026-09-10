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
	// LocalAttached is routes built from the local graph before the route
	// finder was asked (Config.PreferLocalRouteTo): each one is a route-finder
	// query that did not happen.
	LocalAttached int64 `json:"local_attached"`
	// RouteFinderQueries is route-finder requests made (one per attempt).
	RouteFinderQueries int64 `json:"route_finder_queries"`
	// LocalFallback is routes built locally after the route finder missed or
	// ran out of retries.
	LocalFallback int64 `json:"local_fallback"`
}

type routeSourceCounters struct {
	localAttached atomic.Int64
	rfQueries     atomic.Int64
	localFallback atomic.Int64
}

// RouteSourceStats returns the counters.
func (r *router) RouteSourceStats() RouteSourceStats {
	return RouteSourceStats{
		LocalAttached:      r.routeSource.localAttached.Load(),
		RouteFinderQueries: r.routeSource.rfQueries.Load(),
		LocalFallback:      r.routeSource.localFallback.Load(),
	}
}
