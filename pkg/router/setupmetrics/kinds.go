// Package setupmetrics pkg/router/setupmetrics/kinds.go c2-net-routing
//
// Which SETUP PATH each request took, and what batching bought.
//
// A route can reach a setup node three ways today and the /stats snapshot could
// not tell them apart: the legacy single DialRouteGroup (what production runs —
// routing.enable_cascade_route_setup defaults off), the batched
// DialRouteGroupBatch added alongside it, and the source-driven cascade, where
// the node only SIGNS and the source injects the cascade down its own
// transports. "total_requests 54" said nothing about which of the three, so an
// operator could not see whether a deployed client had negotiated the batch
// form at all.
//
// RequestsByKind answers that, RoutesPerBatch shows the batch sizes actually
// arriving, and PerHopRPCsSaved is the concrete coalescing win: the per-hop RPCs
// the batched path did NOT issue because one id reservation and one intermediary
// install covered several routes.
package setupmetrics

// SetupKind names a route-setup path. Values are lowercase-snake so they render
// as JSON object keys.
type SetupKind string

// The setup paths a request can take.
const (
	// SetupKindSingle is one BidirectionalRoute per DialRouteGroup request —
	// the legacy path, and what the live deployment uses today.
	SetupKindSingle SetupKind = "single"
	// SetupKindBatch is one DialRouteGroupBatch carrying N routes to one
	// destination, with the per-hop work coalesced.
	SetupKindBatch SetupKind = "batch"
	// SetupKindCascadeSign is the source-driven cascade: the node signs the
	// reserve/install cascades and the SOURCE injects them down its own
	// transports. Counted once per signing request pair.
	SetupKindCascadeSign SetupKind = "cascade_sign"
)

// BatchStats is the batched-path summary in a StatsSnapshot. It is present even
// when no batch has arrived (all zeroes), so an operator can tell "no client
// has negotiated the batch form" from "this field does not exist on this
// build".
type BatchStats struct {
	// Batches is how many batched requests were handled.
	Batches uint64 `json:"batches"`
	// Routes is the total number of routes those batches carried.
	Routes uint64 `json:"routes"`
	// Installed is how many of those routes came back installed — a batch
	// returns per-route results and partial success is normal.
	Installed uint64 `json:"installed"`
	// RoutesPerBatch is a histogram of batch sizes: routes-in-the-batch →
	// number of batches of that size.
	RoutesPerBatch map[int]uint64 `json:"routes_per_batch"`
	// PerHopRPCsSaved is the running total of per-hop RPCs the coalescing
	// removed — the id-reservation and intermediary-install calls the batch
	// did NOT have to issue because one call covered several routes.
	PerHopRPCsSaved uint64 `json:"per_hop_rpcs_saved"`
}

// RecordSetupKind counts one request against a setup path. routes is how many
// routes the request carried (1 for single and cascade-sign), which is what
// makes "requests by kind" comparable with "routes by kind".
func (c *Collector) RecordSetupKind(kind SetupKind, routes int) {
	if routes <= 0 {
		routes = 1
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.requestsByKind == nil {
		c.requestsByKind = make(map[SetupKind]uint64, 3)
		c.routesByKind = make(map[SetupKind]uint64, 3)
	}
	c.requestsByKind[kind]++
	c.routesByKind[kind] += uint64(routes) //nolint:gosec // bounded by MaxBatchRoutes
}

// RecordBatch records one completed batch: how many routes it carried, how many
// installed, and how many per-hop RPCs the coalescing saved.
func (c *Collector) RecordBatch(routes, installed, rpcsSaved int) {
	if routes <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.routesPerBatch == nil {
		c.routesPerBatch = make(map[int]uint64, 8)
	}
	c.batches++
	c.batchRoutes += uint64(routes)       //nolint:gosec // bounded by MaxBatchRoutes
	c.batchInstalled += uint64(installed) //nolint:gosec // bounded by routes
	c.routesPerBatch[routes]++
	if rpcsSaved > 0 {
		c.perHopRPCsSaved += uint64(rpcsSaved) //nolint:gosec // bounded by the batch's hop count
	}
}

// batchStatsLocked copies the batched-path summary out for Snapshot.
func (c *Collector) batchStatsLocked() BatchStats {
	s := BatchStats{
		Batches:         c.batches,
		Routes:          c.batchRoutes,
		Installed:       c.batchInstalled,
		PerHopRPCsSaved: c.perHopRPCsSaved,
		RoutesPerBatch:  make(map[int]uint64, len(c.routesPerBatch)),
	}
	for k, v := range c.routesPerBatch {
		s.RoutesPerBatch[k] = v
	}
	return s
}

// kindCountsLocked copies the per-kind request and route counters out.
func (c *Collector) kindCountsLocked() (requests, routes map[SetupKind]uint64) {
	requests = make(map[SetupKind]uint64, len(c.requestsByKind))
	routes = make(map[SetupKind]uint64, len(c.routesByKind))
	for k, v := range c.requestsByKind {
		requests[k] = v
	}
	for k, v := range c.routesByKind {
		routes[k] = v
	}
	return requests, routes
}
