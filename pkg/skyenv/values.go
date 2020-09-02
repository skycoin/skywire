package skyenv

import (
	"time"

	"github.com/skycoin/dmsg/cipher"
)

// Constants for default services.
const (
	DefaultTpDiscAddr          = "http://transport.discovery.skywire.skycoin.com"
	DefaultDmsgDiscAddr        = "http://dmsg.discovery.skywire.skycoin.com"
	DefaultServiceDiscAddr     = "http://service.discovery.skycoin.com"
	DefaultRouteFinderAddr     = "http://routefinder.skywire.skycoin.com"
	DefaultUptimeTrackerAddr   = "http://uptime-tracker.skywire.skycoin.com"
	DefaultAddressResolverAddr = "http://address.resolver.skywire.skycoin.com"
	DefaultSetupPK             = "0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557"
)

// Constants for testing deployment.
const (
	TestTpDiscAddr          = "http://transport.discovery.skywire.cc"
	TestDmsgDiscAddr        = "http://dmsg.discovery.skywire.cc"
	TestServiceDiscAddr     = "http://service.discovery.skywire.cc"
	TestRouteFinderAddr     = "http://routefinder.skywire.cc"
	TestUptimeTrackerAddr   = "http://uptime.tracker.skywire.cc"
	TestAddressResolverAddr = "http://address.resolver.skywire.cc"
	TestSetupPK             = "026c5a07de617c5c488195b76e8671bf9e7ee654d0633933e202af9e111ffa358d"
)

// Dmsg port constants.
// TODO(evanlinjin): Define these properly. These are currently random.
const (
	DmsgCtrlPort       uint16 = 7   // Listening port for dmsgctrl protocol (similar to TCP Echo Protocol).
	DmsgSetupPort      uint16 = 36  // Listening port of a setup node.
	DmsgAwaitSetupPort uint16 = 136 // Listening port of a visor for setup operations.
	DmsgTransportPort  uint16 = 45  // Listening port of a visor for incoming transports.
	DmsgHypervisorPort uint16 = 46  // Listening port of a visor for incoming hypervisor connections.
)

// Default dmsgpty constants.
const (
	DmsgPtyPort uint16 = 22

	DefaultDmsgPtyCLINet    = "unix"
	DefaultDmsgPtyCLIAddr   = "/tmp/dmsgpty.sock"
	DefaultDmsgPtyWhitelist = "./dmsgpty/whitelist.json"
)

// Default STCP constants.
const (
	DefaultSTCPAddr = ":7777"
)

// Default skywire app constants.
const (
	SkychatName        = "skychat"
	SkychatPort uint16 = 1
	SkychatAddr        = ":8001"

	SkysocksName        = "skysocks"
	SkysocksPort uint16 = 3

	SkysocksClientName        = "skysocks-client"
	SkysocksClientPort uint16 = 13
	SkysocksClientAddr        = ":1080"

	VPNServerName        = "vpn-server"
	VPNServerPort uint16 = 44

	VPNClientName = "vpn-client"
	// TODO(darkrengarius): this one's not needed for the app to run but lack of it causes errors
	VPNClientPort uint16 = 43
)

// RPC constants.
const (
	DefaultRPCAddr      = "localhost:3435"
	DefaultRPCTimeout   = 20 * time.Second
	TransportRPCTimeout = 1 * time.Minute
	UpdateRPCTimeout    = 6 * time.Hour // update requires huge timeout
)

// Default skywire app server and discovery constants
const (
	DefaultAppSrvAddr     = "localhost:5505"
	AppDiscUpdateInterval = 30 * time.Second
	DefaultAppLocalPath   = "./local"
	DefaultAppBinPath     = "./apps"
	DefaultLogLevel       = "info"
)

// Default routing constants
const (
	DefaultTpLogStore = "./transport_logs"
)

// Default hypervisor constants
const (
	DefaultHypervisorDB = ".skycoin/hypervisor/users.db"
)

// MustPK unmarshals string PK to cipher.PubKey. It panics if unmarshaling fails.
func MustPK(pk string) cipher.PubKey {
	var sPK cipher.PubKey
	if err := sPK.UnmarshalText([]byte(pk)); err != nil {
		panic(err)
	}

	return sPK
}
