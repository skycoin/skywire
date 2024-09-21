// Package skywire skywire.go
package skywire

import (
	_ "embed"
	"encoding/json"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

/*
Embedded Deployment Defaults

Change the contents of the files to embed updated values

Vendor the commit of the change in any repo which depends on them
*/

//go:embed services-config.json
var ServicesJSON []byte

//go:embed dmsghttp-config.json
var DmsghttpJSON []byte

// Wrapper struct for the outer JSON
type EnvServices struct {
	Test json.RawMessage `json:"test"`
	Prod json.RawMessage `json:"prod"`
}

const (
	ConfService     = "http://conf.skywire.skycoin.com"
	ConfServiceTest = "http://conf.skywire.dev"
)

// Services are subdomains and IP addresses of the skywire services
type Services struct {
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
}
