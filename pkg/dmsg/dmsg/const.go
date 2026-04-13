// Package dmsg pkg/dmsg/const.go
package dmsg

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"regexp"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// Constants.
const (
	DefaultMinSessions = 1

	DefaultUpdateInterval = time.Minute

	DefaultMaxSessions = 2048

	DefaultDmsgHTTPPort = uint16(80)

	DefaultOfficialDmsgServerType = "official"

	DefaultCommunityDmsgServerType = "community"
)

// DmsghttpJSON is services-config.json embedded in deployment.ServicesJSON
var DmsghttpJSON = deployment.ServicesJSON

// Prod is the production deployment dmsghttp-config.json services
var Prod DmsghttpConfig

// Test is the test deployment dmsghttp-config.json services
var Test DmsghttpConfig

// DiscURL returns the URL of the dmsg discovery service
func DiscURL(testenv bool) string {
	if testenv {
		return deployment.Test.DmsgDiscovery
	}
	return deployment.Prod.DmsgDiscovery
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

// DmsghttpConfig is the struct that corresponds to the _dmsg fields of services-config.json
type DmsghttpConfig struct {
	DmsgServers        []disc.Entry `json:"dmsg_servers"`
	DmsgDiscovery      string       `json:"dmsg_discovery_dmsg"`
	TransportDiscovery string       `json:"transport_discovery_dmsg"`
	AddressResolver    string       `json:"address_resolver_dmsg"`
	RouteFinder        string       `json:"route_finder_dmsg"`
	UptimeTracker      string       `json:"uptime_tracker_dmsg"`
	ServiceDiscovery   string       `json:"service_discovery_dmsg"`
}

func init() {
	err := InitConfig()
	if err != nil {
		log.Panic(err)
	}
}

// InitConfig initialized the config
func InitConfig() error {
	var envServices deployment.EnvServices
	err := json.Unmarshal(DmsghttpJSON, &envServices)
	if err != nil {
		return err
	}
	if envServices.Prod != nil {
		err = json.Unmarshal(envServices.Prod, &Prod)
		if err != nil {
			return err
		}
		Prod.DmsgServers, err = shuffleServers(Prod.DmsgServers)
		if err != nil {
			return err
		}
	}
	if envServices.Test != nil {
		err = json.Unmarshal(envServices.Test, &Test)
		if err != nil {
			return err
		}
	}
	return nil
}

func shuffleServers(in []disc.Entry) ([]disc.Entry, error) {
	n := len(in)
	for i := n - 1; i > 0; i-- {
		jBig, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return nil, fmt.Errorf("shuffleServers: %w", err)
		}
		j := int(jBig.Int64())
		in[i], in[j] = in[j], in[i]
	}
	return in, nil
}
