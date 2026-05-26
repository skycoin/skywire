// Package deployment github.com/skycoin/skywire/deployment/config.go
//
// The init() that populates Prod / Test / ProdConf / TestConf lives
// in two build-tag-gated files: config_native.go (//go:build !js)
// unmarshals the embedded services-config.json via encoding/json
// with SKYDEPLOY override support; config_js.go (//go:build js)
// copies from static Go literals in data_static_js.go (generated
// from the same JSON by deployment/internal/gen — see that
// package's doc for the why-and-how). Reason for the split:
// encoding/json pulls reflect.unsafe_New / mapassign / etc. that
// TinyGo's stdlib doesn't provide, which blocked TinyGo from
// compiling the install-page WASM (pkg/skywireconfig/genvisor +
// autoconfigcmd).
package deployment

//go:generate go run ./cmd/gen

import (
	_ "embed"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

/*
Embedded Deployment Defaults

services-config.json contains the complete deployment configuration including
both HTTP and DMSG endpoints. The _dmsg suffixed fields contain dmsg:// URLs
for the same services, and dmsg_servers lists the DMSG servers for bootstrapping.

Set SKYDEPLOY=/path/to/config.json to override the embedded defaults with a
custom deployment configuration (e.g., for private networks or testing).

*/

// ServicesJSON is the deployment configuration. By default this is the embedded
// services-config.json. If the SKYDEPLOY environment variable is set to a file
// path, that file is loaded instead at init time.
//
//go:embed services-config.json
var ServicesJSON []byte

// EnvServices is defined in config_native.go (lives there because
// its json.RawMessage fields need encoding/json, which is
// build-tag-gated off the WASM path). The type is still exported
// for callers under the !js build tag that need to read JSON
// fragments — see config_native.go for the definition.

// DmsgServerEntry represents a DMSG server with its public key and address.
// This is a simplified representation that avoids importing dmsg/disc.
// Use ToDiscEntries() to convert to []*disc.Entry when needed.
type DmsgServerEntry struct {
	Static string `json:"static"`
	Server struct {
		Address string `json:"address"`
	} `json:"server"`
}

// HasDmsgServers returns true if the deployment has DMSG server entries.
func (s *Services) HasDmsgServers() bool {
	return len(s.DmsgServers) > 0
}

// DmsgServerEntriesToDisc converts a list of DmsgServerEntry to
// []*disc.Entry suitable for direct.NewClient / direct.GetAllEntries.
// Entries whose Static field is not a valid public key are skipped
// silently — the deployment file is the source of truth and a single
// malformed entry should not abort the whole preload.
func DmsgServerEntriesToDisc(in []DmsgServerEntry) []*disc.Entry {
	if len(in) == 0 {
		return nil
	}
	entries := make([]*disc.Entry, 0, len(in))
	for _, srv := range in {
		var pk cipher.PubKey
		if err := pk.Set(srv.Static); err != nil {
			continue
		}
		entries = append(entries, &disc.Entry{
			Static: pk,
			Server: &disc.Server{Address: srv.Server.Address},
		})
	}
	return entries
}

// ToDiscEntries is the method form of DmsgServerEntriesToDisc, scoped
// to the receiver's DmsgServers list.
func (s *Services) ToDiscEntries() []*disc.Entry {
	return DmsgServerEntriesToDisc(s.DmsgServers)
}

// HasDmsgEndpoints returns true if the deployment has DMSG service endpoints.
func (s *Services) HasDmsgEndpoints() bool {
	return s.DmsgDiscoveryDmsg != ""
}

// Services are URLs, IP addresses, and public keys of the skywire services as deployed.
// HTTP fields contain plain HTTP URLs, _dmsg fields contain dmsg:// URLs for the same services.
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
	ConfDmsg               string            `json:"conf_dmsg,omitempty"`
	DmsgServers            []DmsgServerEntry `json:"dmsg_servers,omitempty"`
	DmsgDiscoveryDmsg      string            `json:"dmsg_discovery_dmsg,omitempty"`
	TransportDiscoveryDmsg string            `json:"transport_discovery_dmsg,omitempty"`
	AddressResolverDmsg    string            `json:"address_resolver_dmsg,omitempty"`
	RouteFinderDmsg        string            `json:"route_finder_dmsg,omitempty"`
	UptimeTrackerDmsg      string            `json:"uptime_tracker_dmsg,omitempty"`
	ServiceDiscoveryDmsg   string            `json:"service_discovery_dmsg,omitempty"`
	// Reward system
	RewardSystem     string `json:"reward_system,omitempty"`
	RewardSystemDmsg string `json:"reward_system_dmsg,omitempty"`
}

// Conf is the configuration URL for the deployment which may be fetched on `skywire cli config gen`
type Conf struct {
	Conf string `json:"conf,omitempty"`
}

// Prod is the production deployment services
var Prod Services

// ProdConf is the service configuration address / URL for the skywire production deployment
var ProdConf Conf

// Test is the test deployment services
var Test Services

// TestConf is the service configuration address / URL for the skywire test deployment
var TestConf Conf

// init() that populates Prod / Test / ProdConf / TestConf lives in
// the build-tag-gated config_native.go (!js, uses encoding/json
// + SKYDEPLOY override) and config_js.go (js, copies from the
// generated static literals). See the package doc above for the
// rationale on the split.
