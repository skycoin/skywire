// Package dmsg pkg/dmsg/const.go
package dmsg

import (
	"encoding/json"
	"log"
	"time"

	"github.com/skycoin/skywire"

	"github.com/skycoin/dmsg/pkg/disc"
)

// Constants.
const (
	DefaultMinSessions = 1

	DefaultUpdateInterval = time.Minute

	DefaultMaxSessions = 100

	DefaultDmsgHTTPPort = uint16(80)

	DefaultOfficialDmsgServerType = "official"

	DefaultCommunityDmsgServerType = "community"
)

// DmsghttpJSON is dmsghttp-config.json embedded in skywire.DmsghttpJSON
var DmsghttpJSON = skywire.DmsghttpJSON

// Prod is the production deployment dmsghttp-config.json services
var Prod DmsghttpConfig

// Test is the test deployment dmsghttp-config.json services
var Test DmsghttpConfig

// DiscAddr returns the address of the dmsg discovery
func DiscAddr(testenv bool) string {
	if testenv {
		return skywire.Test.DmsgDiscovery
	}
	return skywire.Prod.DmsgDiscovery
}

// DmsghttpConfig is the struct that corresponds to the json data of the dmsghttp-config.json
type DmsghttpConfig struct {
	DmsgServers        []disc.Entry `json:"dmsg_servers"`
	DmsgDiscovery      string       `json:"dmsg_discovery"`
	TransportDiscovery string       `json:"transport_discovery"`
	AddressResolver    string       `json:"address_resolver"`
	RouteFinder        string       `json:"route_finder"`
	UptimeTracker      string       `json:"uptime_tracker"`
	ServiceDiscovery   string       `json:"service_discovery"`
}

func init() {
	err := InitConfig()
	if err != nil {
		log.Panic(err)
	}
}

// InitConfig initialized the config
func InitConfig() error {
	var envServices skywire.EnvServices
	err := json.Unmarshal(DmsghttpJSON, &envServices)
	if err != nil {
		return err
	}
	err = json.Unmarshal(envServices.Prod, &Prod)
	if err != nil {
		return err
	}
	err = json.Unmarshal(envServices.Test, &Test)
	if err != nil {
		return err
	}
	return nil
}
