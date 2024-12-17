// Package skywire github.com/skycoin/skywire/skywire.go
//
//go:generate go run cmd/gen/gen.go -jo arches.json
package skywire

import (
	_ "embed"
	"encoding/json"
	"log"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

/*
Embedded Deployment Defaults

Change the contents of services-config.json and / or dmsghttp-config.json to embed updated values
*/

// ServicesJSON is the embedded services-config.json file
//
//go:embed services-config.json
var ServicesJSON []byte

// DmsghttpJSON is the embedded dmsghttp-config.json file
//
//go:embed dmsghttp-config.json
var DmsghttpJSON []byte

// ArchesJSON is the embedded arches.json file
// go run cmd/gen/gen.go -jo arches.json
// ["amd64","arm64","386","arm","ppc64","riscv64","wasm","loong64","mips","mips64","mips64le","mipsle","ppc64le","s390x"]
//
//go:embed arches.json
var ArchesJSON []byte

// Architectures is an array of GOARCH architectures
var Architectures []string

// MainnetRules is the mainnet_rules.md ; it shouldn't have to be embedded here but goland doesn't allow 'embed ../../../../mainnet_rules.md'
// print the mainnet rules with `skywire cli reward rules`
//
//go:embed mainnet_rules.md
var MainnetRules string

// EnvServices is the wrapper struct for the outer JSON - i.e. 'prod' or 'test' deployment config
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
	err := json.Unmarshal(ArchesJSON, &Architectures)
	if err != nil {
		log.Panic("arches.json ", err)
	}
	var js interface{}
	err = json.Unmarshal([]byte(ServicesJSON), &js)
	if err != nil {
		log.Panic("services-config.json ", err)
	}
	err = json.Unmarshal([]byte(DmsghttpJSON), &js)
	if err != nil {
		log.Panic("dmsghttp-config.json ", err)
	}
	var envServices EnvServices
	err = json.Unmarshal(ServicesJSON, &envServices)
	if err != nil {
		log.Panic(err)
	}
	err = json.Unmarshal(envServices.Prod, &Prod)
	if err != nil {
		log.Panic(err)
	}
	err = json.Unmarshal(envServices.Prod, &ProdConf)
	if err != nil {
		log.Panic(err)
	}
	err = json.Unmarshal(envServices.Test, &Test)
	if err != nil {
		log.Panic(err)
	}
	err = json.Unmarshal(envServices.Test, &TestConf)
	if err != nil {
		log.Panic(err)
	}
}
