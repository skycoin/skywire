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

// PkgConfig struct contains paths specific to the linux packages
type PkgConfig struct {
	Launcher struct {
		BinPath string `json:"bin_path"`
	} `json:"launcher"`
	LocalPath    string `json:"local_path"`
	DmsghttpPath string `json:"dmsghttp_path"`
	Hypervisor   struct {
		DbPath     string `json:"db_path"`
		EnableAuth bool   `json:"enable_auth"`
	} `json:"hypervisor"`
	//		TLSCertFile string `json:"tls_cert_file"`
	//		TLSKeyFile  string `json:"tls_key_file"`
}

// PackageLocalPath is the path to local directory
func PackageConfig() PkgConfig {
	var pkgconfig PkgConfig
	pkgconfig.Launcher.BinPath = "/opt/skywire/apps"
	pkgconfig.LocalPath = "/opt/skywire/local"
	pkgconfig.DmsghttpPath = "/opt/skywire/dmsghttp-config.json"
	pkgconfig.Hypervisor.DbPath = "/opt/skywire/users.db" //permissions errors if the process is not run as root.
	pkgconfig.Hypervisor.EnableAuth = true
	return pkgconfig
}

// PackageDmsgPtyWhiteList gets dmsgpty whitelist path for installed Skywire.
func PackageDmsgPtyWhiteList() string {
	return filepath.Join(PackageSkywirePath(), "dmsgpty", "whitelist.json")
}

// MustPK unmarshals string PK to cipher.PubKey. It panics if unmarshaling fails.
func MustPK(pk string) cipher.PubKey {
	var sPK cipher.PubKey
	if err := sPK.UnmarshalText([]byte(pk)); err != nil {
		panic(err)
	}

	return sPK
}
