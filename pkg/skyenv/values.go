package skyenv

import (
	"path/filepath"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

// ConfigName is the default config name. Updated by setting config file path.
var ConfigName = "skywire-config.json"

// DMSGHTTPPath path to dmsghttp-config.json
var DMSGHTTPPath = "dmsghttp-config.json"

// DMSGHTTPURL is URL of dmsghttp-config.json on github
var DMSGHTTPURL = "https://raw.githubusercontent.com/skycoin/skywire/develop/dmsghttp-config.json"

// Constants for skywire root directories.
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

// Dmsgpty constants.
const (
	DmsgPtyPort   uint16 = 22
	DmsgPtyCLINet        = "unix"
)

// Skywire-TCP constants.
const (
	STCPAddr = ":7777"
)

// DmsgPtyCLIAddr determines CLI address per each platform
func DmsgPtyCLIAddr() string {
	return CLIAddr()
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
	RPCAddr             = "localhost:3435"
	RPCTimeout          = 20 * time.Second
	TransportRPCTimeout = 1 * time.Minute
	UpdateRPCTimeout    = 6 * time.Hour // update requires huge timeout
)

// Default skywire app server and discovery constants
const (
	AppSrvAddr                = "localhost:5505"
	ServiceDiscUpdateInterval = time.Minute
	AppBinPath                = "./apps"
	LogLevel                  = "info"
)

// Routing constants
const (
	TpLogStore = "./transport_logs"
)

// Local constants
const (
	LocalPath = "./local"
)

// Default hypervisor constants
const (
	HypervisorDB      = ".skycoin/hypervisor/users.db"
	EnableAuth        = false
	PackageEnableAuth = true
	EnableTLS         = false
	TLSKey            = "./ssl/key.pem"
	TLSCert           = "./ssl/cert.pem"
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
	LocalPath  string `json:"local_path"`
	Hypervisor struct {
		DbPath     string `json:"db_path"`
		EnableAuth bool   `json:"enable_auth"`
	} `json:"hypervisor"`
	//		TLSCertFile string `json:"tls_cert_file"`
	//		TLSKeyFile  string `json:"tls_key_file"`
}

// DmsgPtyWhiteList gets dmsgpty whitelist path for installed Skywire.
func DmsgPtyWhiteList() string {
	return filepath.Join(SkywirePath, "dmsgpty", "whitelist.json")
}

// MustPK unmarshals string PK to cipher.PubKey. It panics if unmarshaling fails.
func MustPK(pk string) cipher.PubKey {
	var sPK cipher.PubKey
	if err := sPK.UnmarshalText([]byte(pk)); err != nil {
		panic(err)
	}

	return sPK
}
