// Package skyenv defines variables and constants
package skyenv

import (
	"time"
)

const (
	// config file constants

	// ConfigName is the default config name. Updated by setting config file path.
	ConfigName = "skywire-config.json"
	// DMSGHTTPName is the default dmsghttp config name
	DMSGHTTPName = "dmsghttp-config.json"
	// SERVICESName is the default services config name - should be the same contents as conf.skywire.skycoin.com or hardcoded fallback in skywire-utilities/pkg/skyenv
	SERVICESName = "services-config.json"

	// Dmsg port constants.
	// TODO(evanlinjin): Define these properly. These are currently random.

	DmsgCtrlPort           uint16 = 7   // DmsgCtrlPort Listening port for dmsgctrl protocol (similar to TCP Echo Protocol). //nolint
	DmsgSetupPort          uint16 = 36  // DmsgSetupPort Listening port of a setup node.
	DmsgHypervisorPort     uint16 = 46  // DmsgHypervisorPort Listening port of a hypervisor for incoming RPC visor connections over dmsg.
	DmsgTransportSetupPort uint16 = 47  // DmsgTransportSetupPort Listening port for transport setup RPC over dmsg.
	DmsgAwaitSetupPort     uint16 = 136 // DmsgAwaitSetupPort Listening port of a visor for setup operations.

	// Transport port constants.

	TransportPort     uint16 = 45   // TransportPort Listening port of a visor for incoming transports.
	PublicAutoconnect        = true // PublicAutoconnect ...

	// Dmsgpty constants.

	DmsgPtyPort   uint16 = 22     // DmsgPtyPort ...
	DmsgPtyCLINet        = "unix" // DmsgPtyCLINet ...

	// Skywire-TCP constants.

	STCPAddr = ":7777" // STCPAddr ...

	// Default skywire app constants.

	SkychatName         = "skychat"  // SkychatName ...
	SkychatPort  uint16 = 1          // SkychatPort ...
	SkychatAddr         = ":8001"    // SkychatAddr ...
	PingTestName        = "pingtest" // PingTestName ...
	PingTestPort uint16 = 2          // PingTestPort ...
	SkysocksName        = "skysocks" // SkysocksName ...
	SkysocksPort uint16 = 3          // SkysocksPort ...

	SkysocksClientName        = "skysocks-client" // SkysocksClientName ...
	SkysocksClientPort uint16 = 13                // SkysocksClientPort ...
	SkysocksClientAddr        = ":1080"           // SkysocksClientAddr ...

	VPNServerName        = "vpn-server" // VPNServerName ...
	VPNServerPort uint16 = 44           // VPNServerPort ...

	VPNClientName = "vpn-client" // VPNClientName ...

	// TODO(darkrengarius): this one's not needed for the app to run but lack of it causes errors

	VPNClientPort     uint16 = 43                   // VPNClientPort ...
	ExampleServerName        = "example-server-app" // ExampleServerName ...
	ExampleServerPort uint16 = 45                   // ExampleServerPort ...
	ExampleClientName        = "example-client-app" // ExampleClientName ...
	ExampleClientPort uint16 = 46                   // ExampleClientPort ...
	SkyPingName              = "sky-ping"           // SkyPingName ...
	SkyPingPort       uint16 = 48                   // SkyPingPort ...

	// RPC constants.

	RPCAddr             = "localhost:3435" // RPCAddr ...
	RPCTimeout          = 20 * time.Second // RPCTimeout ...
	TransportRPCTimeout = 1 * time.Minute  // TransportRPCTimeout ...
	UpdateRPCTimeout    = 6 * time.Hour    // UpdateRPCTimeout update requires huge timeout

	// Default skywire app server and discovery constants

	AppSrvAddr                = "localhost:5505" // AppSrvAddr ...
	ServiceDiscUpdateInterval = time.Minute      // ServiceDiscUpdateInterval ...
	AppBinPath                = "./"             // AppBinPath ...
	LogLevel                  = "info"           // LogLevel ...

	// Routing constants

	TpLogStore = "transport_logs" // TpLogStore ...
	Custom     = "custom"         // Custom ...

	// LocalPath constants
	LocalPath = "./local"

	// Default hypervisor constants

	HypervisorDB      = ".skycoin/hypervisor/users.db" //HypervisorDB ...
	EnableAuth        = false                          // EnableAuth ...
	PackageEnableAuth = true                           // PackageEnableAuth ...
	EnableTLS         = false                          // EnableTLS ...
	TLSKey            = "./ssl/key.pem"                // TLSKey ...
	TLSCert           = "./ssl/cert.pem"               // TLSCert ...

	// IPCShutdownMessageType sends IPC shutdown message type
	IPCShutdownMessageType = 68

	// IsPublic advertises the visor in the service discovery
	IsPublic = false

	// RewardFile is the name of the file containing skycoin rewards address and privacy setting
	RewardFile string = "reward.txt"

	// NodeInfo is the name of the survey file
	NodeInfo string = "node-info.json"
)

// SkywireConfig returns the full path to the package config
func SkywireConfig() string {
	return SkywirePath + "/" + ConfigJSON
}

// PkgConfig struct contains paths specific to the installation
type PkgConfig struct {
	LauncherBinPath string `json:"launcher"`
	LocalPath       string `json:"local_path"`
	Hypervisor      `json:"hypervisor"`
	//		TLSCertFile string `json:"tls_cert_file"`
	//		TLSKeyFile  string `json:"tls_key_file"`
}

// LauncherBinPath struct contains the BinPath specific to the installation
type LauncherBinPath struct {
	BinPath string `json:"bin_path"`
}

// Hypervisor struct contains Hypervisor paths specific to the installation
type Hypervisor struct {
	DbPath     string `json:"db_path"`
	EnableAuth bool   `json:"enable_auth"`
}
