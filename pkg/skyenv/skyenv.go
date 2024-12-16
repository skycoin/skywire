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

	// DmsgCtrlPort Listening port for dmsgctrl protocol (similar to TCP Echo Protocol). //nolint
	DmsgCtrlPort uint16 = 7

	// DmsgSetupPort Listening port of a setup node.
	DmsgSetupPort uint16 = 36

	// DmsgHypervisorPort Listening port of a hypervisor for incoming RPC visor connections over dmsg.
	DmsgHypervisorPort uint16 = 46

	// DmsgTransportSetupPort Listening port for transport setup RPC over dmsg.
	DmsgTransportSetupPort uint16 = 47

	// DmsgAwaitSetupPort Listening port of a visor for setup operations.
	DmsgAwaitSetupPort uint16 = 136

	// Transport port constants.

	// TransportPort Listening port of a visor for incoming transports.
	TransportPort uint16 = 45

	// PublicAutoconnect determines if the visor automatically creates stcpr transports to public visors
	PublicAutoconnect = true

	// Dmsgpty constants.

	// DmsgPtyPort is the dmsg port to listen on for dmsgpty connections
	DmsgPtyPort uint16 = 22

	// DmsgPtyCLINet is the type of cli net used by dmsgpty
	DmsgPtyCLINet = "unix"

	// Skywire-TCP constants.

	// STCPAddr is the address to listen for stcpr or stcp transports
	STCPAddr = ":7777"

	// Default skywire app constants.

	// SkychatName is the name of the skychat app
	SkychatName = "skychat"

	// SkychatPort is the dmsg port used by skychat
	SkychatPort uint16 = 1

	// SkychatAddr is the non-dmsg port used to access the skychat app on localhost
	SkychatAddr = ":8001"

	// PingTestName is the namew of the ping test
	PingTestName = "pingtest"

	// PingTestPort is the port to user for ping tests
	PingTestPort uint16 = 2

	// SkysocksName is the name of the skysocks app
	SkysocksName = "skysocks"

	// SkysocksPort is the skysocks port on dmsg
	SkysocksPort uint16 = 3

	// SkysocksClientName is the skysocks-client app name
	SkysocksClientName = "skysocks-client"

	// SkysocksClientPort is the skysocks-client app dmsg port
	SkysocksClientPort uint16 = 13

	// SkysocksClientAddr is the default port the socks5 proxy client serves on
	SkysocksClientAddr = ":1080"

	// VPNServerName is the name of the vpn server app
	VPNServerName = "vpn-server"

	// VPNServerPort is the vpn server dmsg port
	VPNServerPort uint16 = 44

	// VPNClientName is the name of the vpn client app
	VPNClientName = "vpn-client"

	// TODO(darkrengarius): this one's not needed for the app to run but lack of it causes errors

	// VPNClientPort over dmsg
	VPNClientPort uint16 = 43

	// ExampleServerName is the name of the example server app
	ExampleServerName = "example-server-app"

	// ExampleServerPort is dmsg port of example server app
	ExampleServerPort uint16 = 45

	// ExampleClientName is the name of the example client app
	ExampleClientName = "example-client-app"

	// ExampleClientPort dmsg port of example client app
	ExampleClientPort uint16 = 46

	// SkyForwardingServerName name of sky forwarding server app
	SkyForwardingServerName = "sky-forwarding"

	// SkyForwardingServerPort dmsg port of skyfwd server app
	SkyForwardingServerPort uint16 = 47

	// SkyPingName is the name of the sky ping
	SkyPingName = "sky-ping"

	// SkyPingPort dmsg port of sky ping
	SkyPingPort uint16 = 48

	// RPC constants.

	// RPCAddr for skywire-cli to access skywire-visor
	RPCAddr = "localhost:3435"

	// RPCTimeout timeout of rpc requests
	RPCTimeout = 20 * time.Second

	// TransportRPCTimeout timeout of transport rpc
	TransportRPCTimeout = 1 * time.Minute

	// UpdateRPCTimeout update requires huge timeout - NOTE: this is likely unused
	UpdateRPCTimeout = 6 * time.Hour

	// Default skywire app server and discovery constants

	// AppSrvAddr address of app server
	AppSrvAddr = "localhost:5505"

	// ServiceDiscUpdateInterval update interval for apps in service discovery
	ServiceDiscUpdateInterval = time.Minute

	// AppBinPath is the default path for the apps
	AppBinPath = "./"

	// LogLevel is the default log level of the visor
	LogLevel = "info"

	// Routing constants

	// TpLogStore is where tp logs are stored
	TpLogStore = "transport_logs"

	// Custom path to serve files from dmsghttp log server over dmsg
	Custom = "custom"

	// LocalPath where the visor writes files to
	LocalPath = "./local"

	// Default hypervisor constants

	//HypervisorDB stores the password to access the hypervisor
	HypervisorDB = ".skycoin/hypervisor/users.db"

	// EnableAuth enables auth on the hypervisor UI
	EnableAuth = false

	// PackageEnableAuth is the default auth for package-based installations for hypervisor UI
	PackageEnableAuth = true

	// EnableTLS enables tls for accessing hypervisor ui
	EnableTLS = false

	// TLSKey for access to hvui
	TLSKey = "./ssl/key.pem"

	// TLSCert for access to hvui
	TLSCert = "./ssl/cert.pem"

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
