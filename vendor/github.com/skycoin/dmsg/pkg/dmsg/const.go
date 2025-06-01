// Package dmsg pkg/dmsg/const.go
package dmsg

import (
	"crypto/rand"
	"encoding/json"
	"log"
	"math/big"
	"regexp"
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

// DiscURL returns the URL of the dmsg discovery service
func DiscURL(testenv bool) string {
	if testenv {
		return skywire.Test.DmsgDiscovery
	}
	return skywire.Prod.DmsgDiscovery
}

// DiscAddr returns the dmsg address of the dmsg discovery service in the format "dmsg://<pk>:<port>"
func DiscAddr(testenv bool) string {
	if testenv {
		return Test.DmsgDiscovery
	}
	return Prod.DmsgDiscovery
}

// ExtractPKFromDmsgAddr returns the public key of the dmsg address input in this format in the format "dmsg://<pk>:<port>"
func ExtractPKFromDmsgAddr(input string) string {
	re := regexp.MustCompile(`dmsg://([^:/]+):`)
	match := re.FindStringSubmatch(input)
	if len(match) > 1 {
		return match[1]
	}
	return ""
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
	Prod.DmsgServers = shuffleServers(Prod.DmsgServers)
	err = json.Unmarshal(envServices.Test, &Test)
	if err != nil {
		return err
	}
	return nil
}

func shuffleServers(in []disc.Entry) []disc.Entry {
	n := len(in)
	for i := n - 1; i > 0; i-- {
		jBig, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			panic(err)
		}
		j := int(jBig.Int64())
		in[i], in[j] = in[j], in[i]
	}
	return in
}
