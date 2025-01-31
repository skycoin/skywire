// Package dmsg pkg/dmsg/const.go
package dmsg

import (
	"time"

	"github.com/skycoin/skywire"
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

// DiscAddr returns the address of the dmsg discovery
func DiscAddr(testenv bool) string {
	if testenv {
		return skywire.Test.DmsgDiscovery
	}
	return skywire.Prod.DmsgDiscovery
}

// DmsghttpConfig is the struct that corresponds to the json data of the dmsghttp-config.json
type DmsghttpConfig struct {
	Test struct {
		DmsgServers []struct {
			Static string `json:"static"`
			Server struct {
				Address string `json:"address"`
			} `json:"server"`
		} `json:"dmsg_servers"`
		DmsgDiscovery      string `json:"dmsg_discovery"`
		TransportDiscovery string `json:"transport_discovery"`
		AddressResolver    string `json:"address_resolver"`
		RouteFinder        string `json:"route_finder"`
		UptimeTracker      string `json:"uptime_tracker"`
		ServiceDiscovery   string `json:"service_discovery"`
	} `json:"test"`
	Prod struct {
		DmsgServers []struct {
			Static string `json:"static"`
			Server struct {
				Address string `json:"address"`
			} `json:"server"`
		} `json:"dmsg_servers"`
		DmsgDiscovery      string `json:"dmsg_discovery"`
		TransportDiscovery string `json:"transport_discovery"`
		AddressResolver    string `json:"address_resolver"`
		RouteFinder        string `json:"route_finder"`
		UptimeTracker      string `json:"uptime_tracker"`
		ServiceDiscovery   string `json:"service_discovery"`
	} `json:"prod"`
}
