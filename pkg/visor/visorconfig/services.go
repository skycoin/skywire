// Package visorconfig pkg/visor/visorconfig/services.go
//
// Defines the Services struct that mirrors the deployment.Services
// JSON shape and is embedded in V1. The runtime-only HTTP-fetch
// helper Fetch() lives in services_native.go under a //go:build !js
// constraint so the WASM build graph (genvisor, autoconfigcmd,
// install-page) doesn't pull in net/http — TinyGo 0.41.1's stdlib
// can't compile net/http when our transitive surface widens.
package visorconfig

import (
	"encoding/json"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
)

// EnvServices is the struct for the outer JSON
type EnvServices struct {
	Test json.RawMessage `json:"test"`
	Prod json.RawMessage `json:"prod"`
}

// Services is subdomains and IP addresses of the skywire services.
// This mirrors deployment.Services for use in visor configuration and
// the config gen CLI. Both HTTP and DMSG endpoints are included.
type Services struct {
	// HTTP endpoints
	DmsgDiscovery      string          `json:"dmsg_discovery,omitempty"`
	TransportDiscovery string          `json:"transport_discovery,omitempty"`
	AddressResolver    string          `json:"address_resolver,omitempty"`
	RouteFinder        string          `json:"route_finder,omitempty"`
	RouteSetupNodes    []cipher.PubKey `json:"route_setup_nodes,omitempty"`
	TransportSetupPKs  []cipher.PubKey `json:"transport_setup,omitempty"`
	UptimeTracker      string          `json:"uptime_tracker,omitempty"`
	ServiceDiscovery   string          `json:"service_discovery,omitempty"`
	StunServers        []string        `json:"stun_servers,omitempty"`
	DNSServer          string          `json:"dns_server,omitempty"`
	GeoIP              string          `json:"geoip,omitempty"` // HTTP only (dmsg doesn't preserve client IP)
	SurveyWhitelist    []cipher.PubKey `json:"survey_whitelist,omitempty"`
	// DMSG endpoints (dmsg:// URLs for the same services)
	ConfDmsg               string                       `json:"conf_dmsg,omitempty"`
	DmsgServers            []deployment.DmsgServerEntry `json:"dmsg_servers,omitempty"`
	DmsgDiscoveryDmsg      string                       `json:"dmsg_discovery_dmsg,omitempty"`
	TransportDiscoveryDmsg string                       `json:"transport_discovery_dmsg,omitempty"`
	AddressResolverDmsg    string                       `json:"address_resolver_dmsg,omitempty"`
	RouteFinderDmsg        string                       `json:"route_finder_dmsg,omitempty"`
	UptimeTrackerDmsg      string                       `json:"uptime_tracker_dmsg,omitempty"`
	ServiceDiscoveryDmsg   string                       `json:"service_discovery_dmsg,omitempty"`
	// Reward system
	RewardSystem     string `json:"reward_system,omitempty"`
	RewardSystemDmsg string `json:"reward_system_dmsg,omitempty"`
}

// HasDmsgEndpoints returns true if the services config has DMSG endpoints.
func (s *Services) HasDmsgEndpoints() bool {
	return s != nil && s.DmsgDiscoveryDmsg != ""
}
