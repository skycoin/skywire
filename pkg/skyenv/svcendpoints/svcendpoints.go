// Package svcendpoints is a data-only leaf package describing the notable
// PUBLIC HTTP GET endpoints each deployment service exposes. It carries no
// behavior and no heavy imports — just a static manifest — so the resolving
// proxy's home page (pkg/dmsgweb) can render a per-service endpoint directory
// without depending on the service implementations.
//
// The PATHS here are deployment-INDEPENDENT (they come from each service's chi
// router); the ADDRESSES they pair with are deployment-SPECIFIC and supplied by
// the caller from the visor's resolved config. Keep this file in sync with the
// services' actual routers (pkg/dmsg/discovery/api, pkg/transport-discovery/api,
// pkg/address-resolver/api, pkg/route-finder/api, pkg/service-discovery/api,
// pkg/uptime-tracker/api, and the reward server in
// cmd/skywire-cli/commands/rewards/server).
package svcendpoints

// Endpoint is one notable public HTTP endpoint of a deployment service.
type Endpoint struct {
	// Method is the HTTP method (e.g. "GET", "POST").
	Method string
	// Path is the request path, deployment-independent.
	Path string
	// Desc is a short human-readable description for the directory page.
	Desc string
}

// Manifest maps a stable service id to its notable public endpoints. The ids
// match the resolver's canonical service-alias labels so the home page can pair
// each id with the service's base dmsg address:
//
//	"dmsgd"  — dmsg-discovery
//	"tpd"    — transport-discovery
//	"ar"     — address-resolver
//	"rf"     — route-finder
//	"sd"     — service-discovery
//	"ut"     — uptime-tracker
//	"reward" — reward system
//
// Only PUBLIC, browser-reachable GET endpoints (and a couple of notable POSTs)
// are listed — auth'd registration/heartbeat/deregister and security-nonce
// endpoints are intentionally omitted from the directory.
var Manifest = map[string][]Endpoint{
	// dmsg-discovery — pkg/dmsg/discovery/api/api.go
	"dmsgd": {
		{"GET", "/health", "service health + build info"},
		{"GET", "/dmsg-discovery/entry/{pk}", "client/server entry by PK"},
		{"GET", "/dmsg-discovery/entries", "all client entry PKs"},
		{"GET", "/dmsg-discovery/visorEntries", "all visor client entries"},
		{"GET", "/dmsg-discovery/available_servers", "available dmsg servers"},
		{"GET", "/dmsg-discovery/all_servers", "all dmsg servers"},
		{"GET", "/dmsg-discovery/servers/clients", "clients grouped by server"},
		{"GET", "/dmsg-discovery/server/{pk}/clients", "clients delegated to a server"},
		{"GET", "/uptimes", "visor uptime summaries (?v=v2|v3&visors=)"},
	},
	// transport-discovery — pkg/transport-discovery/api/api.go
	"tpd": {
		{"GET", "/health", "service health + build info"},
		{"GET", "/all-transports", "all registered transports"},
		{"GET", "/all-transports/stats", "all-transports bandwidth stats"},
		{"GET", "/all-transports/per-key-stats", "per-visor transport stats"},
		{"GET", "/transports/id:{id}", "transport by ID"},
		{"GET", "/transports/edge:{edge}", "transports by edge PK"},
		{"GET", "/transports/stats/{edge}", "transport stats by edge"},
		{"GET", "/v3/transports/edge:{edge}", "DHT-aligned transports by edge"},
		{"GET", "/bandwidth/transport/{id}", "transport bandwidth"},
		{"GET", "/bandwidth/visor/{pk}", "visor bandwidth"},
		{"GET", "/metric", "network-wide transport metric"},
		{"GET", "/metric/visor/{pks}", "aggregate metric for visors"},
		{"GET", "/metrics", "all transport metrics"},
		{"GET", "/metrics/{ids}", "metrics for transport IDs"},
		{"GET", "/metrics/visor/{pks}", "metrics for visors"},
		{"GET", "/metrics/uptime", "network transport uptime"},
		{"GET", "/uptimes", "visor uptime summaries"},
		{"GET", "/uptimes/transports", "transport uptime summaries"},
		{"GET", "/version", "version stats"},
		{"GET", "/versions", "per-visor versions"},
		{"GET", "/uptime/now", "service-self uptime (now)"},
	},
	// address-resolver — pkg/address-resolver/api/api.go
	"ar": {
		{"GET", "/health", "service health + build info"},
		{"GET", "/transports", "registered stcpr/sudph bindings"},
		{"GET", "/resolve/{type}/{pk}", "resolve a visor's network address"},
	},
	// route-finder — pkg/route-finder/api/api.go
	"rf": {
		{"GET", "/health", "service health + build info"},
		{"POST", "/routes", "find paired routes between two visors"},
	},
	// service-discovery — pkg/service-discovery/api/api.go
	"sd": {
		{"GET", "/health", "service health + build info"},
		{"GET", "/api/services", "service entries (?type=&version=&country=&quantity=)"},
		{"GET", "/api/services/{addr}", "service entry by address"},
		{"GET", "/uptimes", "visor uptime summaries"},
	},
	// uptime-tracker — pkg/uptime-tracker/api/api.go
	"ut": {
		{"GET", "/health", "service health + build info"},
		{"GET", "/visors", "all tracked visors"},
		{"GET", "/uptimes", "uptime summaries"},
		{"GET", "/uptime/{pk}", "uptime for a single visor"},
		{"GET", "/dashboard", "uptime dashboard chart"},
	},
	// reward system — cmd/skywire-cli/commands/rewards/server
	"reward": {
		{"GET", "/health", "service health"},
		{"GET", "/transports", "transport visualization page"},
		{"GET", "/transports-map", "transport map page"},
		{"GET", "/transport-graph", "transport graph page"},
		{"GET", "/log-collection", "reward log collection overview"},
		{"GET", "/log-collection/tree", "reward log collection tree"},
		{"GET", "/stats/ut", "uptime stats"},
	},
}
