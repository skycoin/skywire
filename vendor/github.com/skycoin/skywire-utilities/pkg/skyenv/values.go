package skyenv

import (
	"path/filepath"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
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
	DefaultNetworkMonitorPKs   = ""
)

// Constants for testing deployment.
const (
	TestTpDiscAddr          = "http://tpd.skywire.dev"
	TestDmsgDiscAddr        = "http://dmsgd.skywire.dev"
	TestServiceDiscAddr     = "http://sd.skywire.dev"
	TestRouteFinderAddr     = "http://rf.skywire.dev"
	TestUptimeTrackerAddr   = "http://ut.skywire.dev"
	TestAddressResolverAddr = "http://ar.skywire.dev"
	TestSetupPK             = "026c2a3e92d6253c5abd71a42628db6fca9dd9aa037ab6f4e3a31108558dfd87cf"
	TestNetworkMonitorPKs   = "0218905f5d9079bab0b62985a05bd162623b193e948e17e7b719133f2c60b92093"
)

// Dmsg port constants.
// TODO(evanlinjin): Define these properly. These are currently random.
const (
	DmsgCtrlPort           uint16 = 7   // Listening port for dmsgctrl protocol (similar to TCP Echo Protocol).
	DmsgSetupPort          uint16 = 36  // Listening port of a setup node.
	DmsgHypervisorPort     uint16 = 46  // Listening port of a hypervisor for incoming RPC visor connections over dmsg.
	DmsgTransportSetupPort uint16 = 47  // Listening port for transport setup RPC over dmsg.
	DmsgAwaitSetupPort     uint16 = 136 // Listening port of a visor for setup operations.
)

// Transport port constants.
const (
	TransportPort uint16 = 45 // Listening port of a visor for incoming transports.
)

// Default dmsgpty constants.
const (
	DmsgPtyPort          uint16 = 22
	DefaultDmsgPtyCLINet        = "unix"
)

// Default Skywire-TCP constants.
const (
	DefaultSTCPAddr = ":7777"
)

// DefaultDmsgPtyCLIAddr determines default CLI address per each platform
func DefaultDmsgPtyCLIAddr() string {
	return DefaultCLIAddr()
}

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
	DefaultAppSrvAddr         = "localhost:5505"
	ServiceDiscUpdateInterval = time.Minute
	DefaultAppBinPath         = DefaultSkywirePath + "/apps"
	DefaultLogLevel           = "info"
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

// Default dmsghttp config constants
const (
	DefaultDMSGHTTPPath = DefaultSkywirePath + "/dmsghttp-config.json"
)

// Default hypervisor constants
const (
	DefaultHypervisorDB      = ".skycoin/hypervisor/users.db"
	DefaultEnableAuth        = false
	DefaultPackageEnableAuth = true
	DefaultEnableTLS         = false
	DefaultTLSKey            = DefaultSkywirePath + "/ssl/key.pem"
	DefaultTLSCert           = DefaultSkywirePath + "/ssl/cert.pem"
)

const (
	// IPCShutdownMessageType sends IPC shutdown message type
	IPCShutdownMessageType = 68
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
	return filepath.Join(appBinPath(), "apps")
}

// PackageDMSGHTTPPath gets dmsghttp path for installed Skywire.
func PackageDMSGHTTPPath() string {
	return filepath.Join(appBinPath(), "dmsghttp-config.json")
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

// GetStunServers gives back deafault Stun Servers
func GetStunServers() []string {
	return []string{
		"172.104.188.139:3478",
		"172.104.59.235:3478",
		"172.104.183.187:3478",
		"139.162.54.63:3478",
		"172.105.115.97:3478",
		"172.104.188.39:3478",
		"172.104.188.140:3478",
		"172.104.40.88:3478",
	}
}
