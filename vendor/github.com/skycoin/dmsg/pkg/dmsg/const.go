// Package dmsg pkg/dmsg/const.go
package dmsg

import (
	"time"
	"encoding/json"
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

func DmsgDiscAddr(testenv bool) string {
	var envServices skywire.EnvServices
	var services skywire.Services
	if err := json.Unmarshal([]byte(skywire.ServicesJSON), &envServices); err == nil {
		if testenv {
			if err := json.Unmarshal(envServices.Prod, &services); err == nil {
				return services.DmsgDiscovery
			}
		} else {
			if err := json.Unmarshal(envServices.Test, &services); err == nil {
				return services.DmsgDiscovery
			}
		}
	}
	return ""
}
