package config

import (
	"encoding/json"
	"log"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

// EnvConfig defines configuration of skywire environment
type EnvConfig struct {
	Description      string                 `json:"description"`
	Runners          RunnersConfig          `json:"runners"`
	ExternalServices ExternalServicesConfig `json:"external_services,omitempty"`
	Skywire          SkywireConfig          `json:"skywire_services"`
	Visors           []VisorConfig          `json:"skywire"`
	Scripts          EnvScripts             `json:"scripts"`
}

// ExternalServicesConfig define configuration of external services
type ExternalServicesConfig struct {
	RedisAddress   string `json:"redis,omitempty"`
	RedisCmd       string `json:"redis_cmd,omitempty"`
	MetricsAddress string `json:"metrics,omitempty"`
}

// RunnersConfig defines how each service is run
type RunnersConfig struct {
	SkywireVisor       string `json:"skywire,omitempty"`
	DmsgDiscovery      string `json:"dmsg_discovery,omitempty"`
	DmsgServer         string `json:"dmsg_server,omitempty"`
	TransportDiscovery string `json:"transport_discovery,omitempty"`
	RouteFinder        string `json:"route_finder,omitempty"`
	SetupNode          string `json:"setup_node,omitempty"`
	AddressResolver    string `json:"address_resolver,omitempty"`
}

// PrintJSON does json-formatting of data
func PrintJSON(data interface{}) string {
	raw, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		log.Fatal(err)
	}
	return string(raw)
}

func (env *EnvConfig) String() string {
	return PrintJSON(env)
}

func _pk(pkHex string) cipher.PubKey {
	var pk cipher.PubKey
	err := pk.Set(pkHex)
	if err != nil {
		log.Fatal(err)
	}
	return pk
}
