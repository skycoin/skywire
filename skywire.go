// Package skywire skywire.go
package skywire

import (
	_ "embed"
	"encoding/json"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

/*
Embedded Deployment Defaults

Change the contents of services-config.json and / or dmsghttp-config.json to embed updated values
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

// Services are URLs, IP addresses, and public keys of the skywire services as deployed
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

// Conf is the configuration URL for the deployment which may be fetched on `skywire cli config gen`
type Conf struct {
	Conf      string          `json:"conf,omitempty"`
}

//Prod is the production deployment services
var Prod Services

//ProdConf is the service configuration address / URL for the skywire production deployment
var ProdConf Conf

//Test is the test deployment services
var Test Services

//TestConf is the service configuration address / URL for the skywire test deployment
var TestConf Conf

func init() {
	var envServices EnvServices
	err := json.Unmarshal(ServicesJSON, &envServices)
	if err != nil {
		panic(err)
	}
	err = json.Unmarshal(envServices.Prod, &Prod)
	if err != nil {
		panic(err)
	}
	err = json.Unmarshal(envServices.Prod, &ProdConf)
	if err != nil {
		panic(err)
	}
	err = json.Unmarshal(envServices.Test, &Test)
	if err != nil {
		panic(err)
	}
	err = json.Unmarshal(envServices.Test, &TestConf)
	if err != nil {
		panic(err)
	}
}
