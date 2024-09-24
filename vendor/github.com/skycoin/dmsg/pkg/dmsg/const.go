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

// DiscAddr returns the address of the dmsg discovery
func DiscAddr(testenv bool) string {
	if testenv {
		return skywire.Prod.DmsgDiscovery

	}
	return skywire.Test.DmsgDiscovery
}
