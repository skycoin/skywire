// Package deployment github.com/skycoin/skywire/deployment/config.go
package deployment

import (
	_ "embed"
	"encoding/json"
	"log"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

/*
Embedded Deployment Defaults

services-config.json contains the complete deployment configuration including
both HTTP and DMSG endpoints. The _dmsg suffixed fields contain dmsg:// URLs
for the same services, and dmsg_servers lists the DMSG servers for bootstrapping.

dmsghttp-config.json is retained for backward compatibility with dmsg imports.
It will be removed once dmsg is updated to read from services-config.json.
*/

// ServicesJSON is the embedded services-config.json file (unified deployment config)
//
//go:embed services-config.json
var ServicesJSON []byte

// DmsghttpJSON is the embedded dmsghttp-config.json file.
// Deprecated: retained for backward compatibility with dmsg package imports.
// Use ServicesJSON with _dmsg suffixed fields instead.
//
//go:embed dmsghttp-config.json
var DmsghttpJSON []byte

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
