// Package svcendpoints is a data-only leaf package describing the notable
// PUBLIC HTTP endpoints each deployment service exposes. It carries no
// behavior and no heavy imports — just a static manifest — so the resolving
// proxy's home page (pkg/dmsgweb) can render a per-service endpoint directory
// (and a small interactive API explorer) without depending on the service
// implementations.
//
// The PATHS here are deployment-INDEPENDENT (they come from each service's chi
// router); the ADDRESSES they pair with are deployment-SPECIFIC and supplied by
// the caller from the visor's resolved config. Keep this file in sync with the
// services' actual routers (pkg/dmsg/discovery/api, pkg/transport-discovery/api,
// pkg/address-resolver/api, pkg/route-finder/api, pkg/service-discovery/api,
// pkg/uptime-tracker/api, and the reward server in
// cmd/skywire-cli/commands/rewards/server).
//
// Path parameters are expressed inline in Path with chi's "{name}" syntax and
// are parsed by the renderer at display time — they need no dedicated field.
// Query parameters and POST request-body hints are declared explicitly (Query,
// Body) so the explorer can surface an input per query param and a body
// textarea per POST. Only params that are verifiable in the service code are
// declared; everything else is left empty.
package svcendpoints

// Param is one query parameter an endpoint accepts.
type Param struct {
	// Name is the query-string key (e.g. "v").
	Name string
	// Desc is a short human-readable description.
	Desc string
	// Example is a representative value, used as the input placeholder.
	Example string
}

// Endpoint is one notable public HTTP endpoint of a deployment service.
type Endpoint struct {
	// Method is the HTTP method (e.g. "GET", "POST").
	Method string
	// Path is the request path, deployment-independent. It may contain
	// chi-style "{name}" path parameters, which the renderer turns into
	// per-param inputs.
	Path string
	// Desc is a short human-readable description for the directory page.
	Desc string
	// Query lists the query parameters the endpoint understands. Empty when
	// the endpoint takes none.
	Query []Param
	// Body is a request-body format hint / example JSON for POST endpoints
	// (used as the textarea placeholder). Empty for endpoints with no body.
	Body string
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
		{Method: "GET", Path: "/health", Desc: "service health + build info"},
		{Method: "GET", Path: "/dmsg-discovery/entry/{pk}", Desc: "client/server entry by PK"},
		{Method: "GET", Path: "/dmsg-discovery/entries", Desc: "all client entry PKs"},
		{Method: "GET", Path: "/dmsg-discovery/visorEntries", Desc: "all visor client entries"},
		{Method: "GET", Path: "/dmsg-discovery/available_servers", Desc: "available dmsg servers"},
		{Method: "GET", Path: "/dmsg-discovery/all_servers", Desc: "all dmsg servers"},
		{Method: "GET", Path: "/dmsg-discovery/servers/clients", Desc: "clients grouped by server"},
		{Method: "GET", Path: "/dmsg-discovery/server/{pk}/clients", Desc: "clients delegated to a server"},
		{Method: "GET", Path: "/uptimes", Desc: "visor uptime summaries", Query: []Param{
			{Name: "v", Desc: "response version: v2 (daily %) or v3 (+ timeline)", Example: "v3"},
			{Name: "visors", Desc: "filter to a ;-separated PK list", Example: "pk1;pk2"},
		}},
	},
	// transport-discovery — pkg/transport-discovery/api/api.go
	"tpd": {
		{Method: "GET", Path: "/health", Desc: "service health + build info"},
		{Method: "GET", Path: "/all-transports", Desc: "all registered transports"},
		{Method: "GET", Path: "/all-transports/stats", Desc: "all-transports bandwidth stats"},
		{Method: "GET", Path: "/all-transports/per-key-stats", Desc: "per-visor transport stats"},
		{Method: "GET", Path: "/transports/id:{id}", Desc: "transport by ID"},
		{Method: "GET", Path: "/transports/edge:{edge}", Desc: "transports by edge PK"},
		{Method: "GET", Path: "/transports/stats/{edge}", Desc: "transport stats by edge"},
		{Method: "GET", Path: "/v3/transports/edge:{edge}", Desc: "DHT-aligned transports by edge"},
		{Method: "GET", Path: "/bandwidth/transport/{id}", Desc: "transport bandwidth"},
		{Method: "GET", Path: "/bandwidth/visor/{pk}", Desc: "visor bandwidth"},
		{Method: "GET", Path: "/metric", Desc: "network-wide transport metric"},
		{Method: "GET", Path: "/metric/visor/{pks}", Desc: "aggregate metric for visors"},
		{Method: "GET", Path: "/metrics", Desc: "all transport metrics"},
		{Method: "GET", Path: "/metrics/{ids}", Desc: "metrics for transport IDs"},
		{Method: "GET", Path: "/metrics/visor/{pks}", Desc: "metrics for visors"},
		{Method: "GET", Path: "/metrics/uptime", Desc: "network transport uptime"},
		{Method: "GET", Path: "/uptimes", Desc: "visor uptime summaries"},
		{Method: "GET", Path: "/uptimes/transports", Desc: "transport uptime summaries"},
		{Method: "GET", Path: "/version", Desc: "version stats"},
		{Method: "GET", Path: "/versions", Desc: "per-visor versions"},
		{Method: "GET", Path: "/uptime/now", Desc: "service-self uptime (now)"},
	},
	// address-resolver — pkg/address-resolver/api/api.go
	"ar": {
		{Method: "GET", Path: "/health", Desc: "service health + build info"},
		{Method: "GET", Path: "/transports", Desc: "registered stcpr/sudph bindings"},
		{Method: "GET", Path: "/resolve/{type}/{pk}", Desc: "resolve a visor's network address (type = stcpr|sudph)"},
	},
	// route-finder — pkg/route-finder/api/api.go
	"rf": {
		{Method: "GET", Path: "/health", Desc: "service health + build info"},
		{Method: "POST", Path: "/routes", Desc: "find paired routes between two visors",
			Body: `{"Edges":[["<src-pk>","<dst-pk>"]],"Opts":{"MinHops":0,"MaxHops":0,"NumRoutes":0}}`},
	},
	// service-discovery — pkg/service-discovery/api/api.go
	"sd": {
		{Method: "GET", Path: "/health", Desc: "service health + build info"},
		{Method: "GET", Path: "/api/services", Desc: "service entries", Query: []Param{
			{Name: "type", Desc: "service type (required)", Example: "vpn"},
			{Name: "version", Desc: "filter by visor version", Example: ""},
			{Name: "country", Desc: "filter by 2-letter country code", Example: "US"},
			{Name: "quantity", Desc: "cap result count (random sample)", Example: "10"},
		}},
		{Method: "GET", Path: "/api/services/{addr}", Desc: "service entry by address", Query: []Param{
			{Name: "type", Desc: "service type (required)", Example: "vpn"},
		}},
		{Method: "GET", Path: "/uptimes", Desc: "visor uptime summaries", Query: []Param{
			{Name: "v", Desc: "response version: v2 or v3", Example: "v3"},
			{Name: "visors", Desc: "filter to a ;-separated PK list", Example: "pk1;pk2"},
		}},
	},
	// uptime-tracker — pkg/uptime-tracker/api/api.go
	"ut": {
		{Method: "GET", Path: "/health", Desc: "service health + build info"},
		{Method: "GET", Path: "/visors", Desc: "all tracked visors"},
		{Method: "GET", Path: "/uptimes", Desc: "uptime summaries", Query: []Param{
			{Name: "visors", Desc: "filter to a ,-separated PK list", Example: "pk1,pk2"},
			{Name: "v", Desc: "response version: v2 (daily history)", Example: "v2"},
			{Name: "status", Desc: "filter by online state: on or off", Example: "on"},
			{Name: "startDate", Desc: "period start (unix seconds)", Example: ""},
			{Name: "endDate", Desc: "period end (unix seconds)", Example: ""},
		}},
		{Method: "GET", Path: "/uptime/{pk}", Desc: "uptime for a single visor"},
		{Method: "GET", Path: "/dashboard", Desc: "uptime dashboard chart", Query: []Param{
			{Name: "length", Desc: "number of months to chart", Example: "6"},
		}},
	},
	// reward system — cmd/skywire-cli/commands/rewards/server
	"reward": {
		{Method: "GET", Path: "/health", Desc: "service health"},
		{Method: "GET", Path: "/transports", Desc: "transport visualization page"},
		{Method: "GET", Path: "/transports-map", Desc: "transport map page"},
		{Method: "GET", Path: "/transport-graph", Desc: "transport graph page"},
		{Method: "GET", Path: "/log-collection", Desc: "reward log collection overview"},
		{Method: "GET", Path: "/log-collection/tree", Desc: "reward log collection tree"},
		{Method: "GET", Path: "/stats/ut", Desc: "uptime stats"},
	},
}
