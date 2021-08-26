package skyenv

import (
	"path/filepath"
	"time"

	"github.com/skycoin/dmsg/cipher"
)

// Constants for skywire root directories.
const (
	DefaultSkywirePath = "."
)

// Constants for old default services.
const (
	OldDefaultTpDiscAddr          = "http://transport.discovery.skywire.skycoin.com"
	OldDefaultDmsgDiscAddr        = "http://dmsg.discovery.skywire.skycoin.com"
	OldDefaultServiceDiscAddr     = "http://service.discovery.skycoin.com"
	OldDefaultRouteFinderAddr     = "http://routefinder.skywire.skycoin.com"
	OldDefaultUptimeTrackerAddr   = "http://uptime-tracker.skywire.skycoin.com"
	OldDefaultAddressResolverAddr = "http://address.resolver.skywire.skycoin.com"
)

// Constants for new default services.
const (
	DefaultTpDiscAddr          = "http://tpd.skywire.skycoin.com"
	DefaultDmsgDiscAddr        = "http://dmsgd.skywire.skycoin.com"
	DefaultServiceDiscAddr     = "http://sd.skycoin.com"
	DefaultRouteFinderAddr     = "http://rf.skywire.skycoin.com"
	DefaultUptimeTrackerAddr   = "http://ut.skywire.skycoin.com"
	DefaultAddressResolverAddr = "http://ar.skywire.skycoin.com"
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
	DmsgCtrlPort           uint16 = 7   // Listening port for dmsgctrl protocol (similar to TCP Echo Protocol).
	DmsgSetupPort          uint16 = 36  // Listening port of a setup node.
	DmsgAwaitSetupPort     uint16 = 136 // Listening port of a visor for setup operations.
	DmsgTransportPort      uint16 = 45  // Listening port of a visor for incoming transports.
	DmsgHypervisorPort     uint16 = 46  // Listening port of a visor for incoming hypervisor connections.
	DmsgTransportSetupPort uint16 = 47
)

// Default dmsgpty constants.
const (
	DmsgPtyPort           uint16 = 22
	DefaultDmsgPtyCLINet         = "unix"
	DefaultDmsgPtyCLIAddr        = "/tmp/dmsgpty.sock"
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
	AppDiscUpdateInterval = time.Minute
	DefaultAppBinPath     = DefaultSkywirePath + "/apps"
	DefaultLogLevel       = "info"
)

// Default routing constants
const (
	DefaultTpLogStore = DefaultSkywirePath + "/transport_logs"
)

// Skybian defaults
const (
	SkybianAppBinPath       = "/usr/bin/apps"
	SkybianDmsgPtyWhiteList = "/var/skywire-visor/dsmgpty/whitelist.json"
	SkybianDmsgPtyCLIAddr   = "/run/skywire-visor/dmsgpty/cli.sock"
	SkybianLocalPath        = "/var/skywire-visor/apps"
	SkybianTpLogStore       = "/var/skywire-visor/transports"
	SkybianEnableTLS        = false
	SkybianDBPath           = "/var/skywire-visor/users.db"
	SkybianTLSKey           = "/var/skywire-visor/ssl/key.pem"
	SkybianTLSCert          = "/var/skywire-visor/ssl/cert.pem"
)

// Default local constants
const (
	DefaultLocalPath = DefaultSkywirePath + "/local"
)

// Default hypervisor constants
const (
	DefaultHypervisorDB = ".skycoin/hypervisor/users.db"
	DefaultEnableAuth   = true
	DefaultEnableTLS    = false
	DefaultTLSKey       = DefaultSkywirePath + "/ssl/key.pem"
	DefaultTLSCert      = DefaultSkywirePath + "/ssl/cert.pem"
)

// PackageLocalPath is the path to local directory
func PackageLocalPath() string {
	return filepath.Join(PackageSkywirePath(), "local")
}

// PackageDmsgPtyCLIAddr is the path to dmsgpty-cli file socket
func PackageDmsgPtyCLIAddr() string {
	return filepath.Join(PackageSkywirePath(), "dmsgpty", "cli.sock")
}

// PackageDBPath is the filepath location to the local db
func PackageDBPath() string {
	return filepath.Join(PackageSkywirePath(), "users.db")
}

// PackageDmsgPtyWhiteList gets dmsgpty whitelist path for installed Skywire.
func PackageDmsgPtyWhiteList() string {
	return filepath.Join(PackageSkywirePath(), "dmsgpty", "whitelist.json")
}

// PackageAppLocalPath gets `.local` path for installed Skywire.
func PackageAppLocalPath() string {
	return filepath.Join(PackageSkywirePath(), "local")
}

// PackageAppBinPath gets apps path for installed Skywire.
func PackageAppBinPath() string {
	return filepath.Join(PackageSkywirePath(), "apps")
}

// PackageTpLogStore gets transport logs path for installed Skywire.
func PackageTpLogStore() string {
	return filepath.Join(PackageSkywirePath(), "transport_logs")
}

// PackageTLSKey gets TLS key path for installed Skywire.
func PackageTLSKey() string {
	return filepath.Join(PackageSkywirePath(), "ssl", "key.pem")
}

// PackageTLSCert gets TLS cert path for installed Skywire.
func PackageTLSCert() string {
	return filepath.Join(PackageSkywirePath(), "ssl", "cert.pem")
}

// MustPK unmarshals string PK to cipher.PubKey. It panics if unmarshaling fails.
func MustPK(pk string) cipher.PubKey {
	var sPK cipher.PubKey
	if err := sPK.UnmarshalText([]byte(pk)); err != nil {
		panic(err)
	}

	return sPK
}
