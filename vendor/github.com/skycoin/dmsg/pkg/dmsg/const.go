// Package dmsg pkg/dmsg/const.go
package dmsg

import (
	"time"

	"github.com/skycoin/skywire-utilities/pkg/skyenv"
)

// Constants.
const (
	DefaultDiscAddr = skyenv.DmsgDiscAddr

	DefaultMinSessions = 1

	DefaultUpdateInterval = time.Minute

	DefaultMaxSessions = 100

	DefaultDmsgHTTPPort = uint16(80)

	DefaultOfficialDmsgServerType = "official"

	DefaultCommunityDmsgServerType = "community"
)
