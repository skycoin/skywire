package dmsg

import "time"

// Constants.
const (
	// TODO(evanlinjin): Reference the production address on release
	DefaultDiscAddr = "http://dmsg.discovery.skywire.cc"

	DefaultMinSessions = 1

	DefaultUpdateInterval = time.Second * 15

	DefaultMaxSessions = 100
)
