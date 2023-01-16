// Package skyenv defines variables and constants
package skyenv

import "time"

const (
	// config file constants
	// ConfigName is the default config name. Updated by setting config file path.
	ConfigName = "skywire-config.json"
	// DMSGHTTPName is the default dmsghttp config name
	DMSGHTTPName = "dmsghttp-config.json"

	// Dmsg port constants.
	// TODO(evanlinjin): Define these properly. These are currently random.
	DmsgCtrlPort           uint16 = 7   // Listening port for dmsgctrl protocol (similar to TCP Echo Protocol).
	DmsgSetupPort          uint16 = 36  // Listening port of a setup node.
	DmsgHypervisorPort     uint16 = 46  // Listening port of a hypervisor for incoming RPC visor connections over dmsg.
	DmsgTransportSetupPort uint16 = 47  // Listening port for transport setup RPC over dmsg.
	DmsgAwaitSetupPort     uint16 = 136 // Listening port of a visor for setup operations.

	// Transport port constants.
	TransportPort     uint16 = 45 // Listening port of a visor for incoming transports.
	PublicAutoconnect        = true

	// Dmsgpty constants.
	DmsgPtyPort   uint16 = 22
	DmsgPtyCLINet        = "unix"

	// Skywire-TCP constants.
	STCPAddr = ":7777"

	// Default skywire app constants.
	SkychatName        = "skychat"
	SkychatPort uint16 = 1
	SkychatAddr        = ":8001"
	PingTestName        = "pingtest"
	PingTestPort uint16 = 2
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
	ExampleServerName              = "example-server-app"
	ExampleServerPort       uint16 = 45
	ExampleClientName              = "example-client-app"
	ExampleClientPort       uint16 = 46
	SkyForwardingServerName        = "sky-forwarding"
	SkyForwardingServerPort uint16 = 47
	SkyPingName                    = "sky-ping"
	SkyPingPort             uint16 = 48


	// RPC constants.
	RPCAddr             = "localhost:3435"
	RPCTimeout          = 20 * time.Second
	TransportRPCTimeout = 1 * time.Minute
	UpdateRPCTimeout    = 6 * time.Hour // update requires huge timeout

	// Default skywire app server and discovery constants
	AppSrvAddr                = "localhost:5505"
	ServiceDiscUpdateInterval = time.Minute
	AppBinPath                = "./apps"
	LogLevel                  = "info"

	// Routing constants
	TpLogStore = "transport_logs"
	Custom     = "custom"

	// Local constants
	LocalPath = "./local"

	// Default hypervisor constants
	HypervisorDB      = ".skycoin/hypervisor/users.db"
	EnableAuth        = false
	PackageEnableAuth = true
	EnableTLS         = false
	TLSKey            = "./ssl/key.pem"
	TLSCert           = "./ssl/cert.pem"

	// IPCShutdownMessageType sends IPC shutdown message type
	IPCShutdownMessageType = 68

	// IsPublic advertises the visor in the service discovery
	IsPublic = false

	// SurveyFile is the name of the survey file
	SurveyFile string = "system.json"

	// SurveySha256 is the name of the survey checksum file
	SurveySha256 string = "system.sha"

	// RewardFile is the name of the file containing skycoin rewards address and privacy setting
	RewardFile string = "reward.txt"
)

// PkgConfig struct contains paths specific to the linux packages
type PkgConfig struct {
	LauncherBinPath `json:"launcher"`
	LocalPath       string `json:"local_path"`
	Hypervisor      `json:"hypervisor"`
	//		TLSCertFile string `json:"tls_cert_file"`
	//		TLSKeyFile  string `json:"tls_key_file"`
}

// Launcher struct contains the BinPath specific to the installation
type LauncherBinPath struct {
	BinPath string `json:"bin_path"`
}

// Hypervisor struct contains Hypervisor paths specific to the linux packages
type Hypervisor struct {
	DbPath     string `json:"db_path"`
	EnableAuth bool   `json:"enable_auth"`
}
