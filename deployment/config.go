// Package deployment github.com/skycoin/skywire/deployment/config.go
package deployment

import (
	_ "embed"
	"encoding/json"
	"log"
	"os"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
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

// EnvServices is the wrapper struct for the outer JSON - i.e. 'prod' or 'test' deployment config
type EnvServices struct {
	Test json.RawMessage `json:"test"`
	Prod json.RawMessage `json:"prod"`
}

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

func init() {
	// SKYDEPLOY overrides the embedded deployment config with a user-supplied file.
	// This supports private networks, corporate deployments, and test environments.
	if path := os.Getenv("SKYDEPLOY"); path != "" {
		data, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			log.Panicf("SKYDEPLOY=%s: %v", path, err) //nolint:gosec
		}
		ServicesJSON = data
	}

	var envServices EnvServices
	err := json.Unmarshal(ServicesJSON, &envServices)
	if err != nil {
		log.Panic("services-config.json: ", err)
	}
	if err = json.Unmarshal(envServices.Prod, &Prod); err != nil {
		log.Panic(err)
	}
	if err = json.Unmarshal(envServices.Prod, &ProdConf); err != nil {
		log.Panic(err)
	}
	if err = json.Unmarshal(envServices.Test, &Test); err != nil {
		log.Panic(err)
	}
	if err = json.Unmarshal(envServices.Test, &TestConf); err != nil {
		log.Panic(err)
	}
}
