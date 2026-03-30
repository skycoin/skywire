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

	// DmsgCtrlPort Listening port for dmsgctrl protocol (similar to TCP Echo Protocol).
	DmsgCtrlPort uint16 = 7

	// DmsgPingPort Listening port for dmsg ping protocol.
	DmsgPingPort uint16 = 8

	// DmsgSetupPort Listening port of a setup node.
	DmsgSetupPort uint16 = 36

	// DmsgHypervisorPort Listening port of a hypervisor for incoming RPC visor connections over dmsg.
	DmsgHypervisorPort uint16 = 46

	// DmsgTransportSetupPort Listening port for transport setup RPC over dmsg.
	DmsgTransportSetupPort uint16 = 47

	// DmsgTransportSetupServicePort Listening port for transport setup service requests over dmsg.
	// This is the port where TPS nodes listen for incoming transport setup requests from visors.
	DmsgTransportSetupServicePort uint16 = 48

	// DmsgAwaitSetupPort Listening port of a visor for setup operations.
	DmsgAwaitSetupPort uint16 = 136

	// Transport port constants.

	// TransportPort Listening port of a visor for incoming transports.
	TransportPort uint16 = 45

	// LatencyProbePort is the Skywire routing port for transport latency probes.
	// Note: same number as DmsgHypervisorPort but different namespace (routing vs DMSG).
	LatencyProbePort uint16 = 46

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

	// VPNClientPort over dmsg
	VPNClientPort uint16 = 43

	// ExampleServerName is the name of the example server app
	ExampleServerName = "example-server-app"

	// ExampleServerPort is dmsg port of example server app
	// Previously 45 — conflicted with TransportPort
	ExampleServerPort uint16 = 55

	// SkyForwardingServerName name of sky forwarding server app (built-in)
	SkyForwardingServerName = "sky-forwarding"

	// SkyForwardingServerPort skynet port of skyfwd server app (built-in)
	// Previously 47 — conflicted with DmsgTransportSetupPort
	SkyForwardingServerPort uint16 = 57

	// SkyPingPort dmsg port of sky ping
	// Previously 48 — conflicted with DmsgTransportSetupServicePort
	SkyPingPort uint16 = 58

	// RPC constants.

	// RPCAddr for skywire-cli to access skywire-visor
	RPCAddr = "localhost:3435"

	// RPCTimeout timeout of rpc requests
	RPCTimeout = 20 * time.Second

	// TransportRPCTimeout timeout of transport rpc
	TransportRPCTimeout = 1 * time.Minute

	// UpdateRPCTimeout is the RPC timeout for the "Update" method (used by rpcClient.Call)
	UpdateRPCTimeout = 6 * time.Hour

	// HealthTimeout defines timeout for /health endpoint calls done from hypervisor.
	HealthTimeout = 5 * time.Second

	// InnerHealthTimeout defines timeout for /health endpoint calls done from visor.
	// Kept less than HealthTimeout so that the outer call completes.
	InnerHealthTimeout = 3 * time.Second

	// DMSG tracker constants.

	// DmsgTrackerUpdateInterval is the interval for updating DMSG client summaries.
	DmsgTrackerUpdateInterval = 30 * time.Second

	// DmsgTrackerUpdateTimeout is the timeout for a single DMSG tracker update.
	DmsgTrackerUpdateTimeout = 10 * time.Second

	// Visor registration constants.

	// PublicVisorRegistrationTimeout is the timeout for registering as a public visor.
	PublicVisorRegistrationTimeout = 10 * time.Minute

	// HTTP server timeout constants (for dmsg and local HTTP servers).

	// HTTPReadTimeout is the maximum duration for reading the entire request.
	HTTPReadTimeout = 30 * time.Second

	// HTTPWriteTimeout is the maximum duration before timing out writes of the response.
	HTTPWriteTimeout = 60 * time.Second

	// HTTPIdleTimeout is the maximum time to wait for the next request when keep-alives are enabled.
	HTTPIdleTimeout = 90 * time.Second

	// HTTPReadHeaderTimeout is the amount of time allowed to read request headers.
	HTTPReadHeaderTimeout = 10 * time.Second

	// Default skywire app server and discovery constants

	// AppSrvAddr address of app server
	AppSrvAddr = "localhost:5505"

	// ServiceDiscUpdateInterval update interval (heartbeat) for apps in service discovery
	ServiceDiscUpdateInterval = 90 * time.Second

	// PublicAutoconnectInterval interval for checking service discovery and connecting to public visors
	PublicAutoconnectInterval = 300 * time.Second

	// AppBinPath is the default path for the apps
	AppBinPath = "./"

	// LogLevel is the default log level of the visor
	LogLevel = "info"

	// Routing constants

	// TpLogStore is where tp logs are stored
	TpLogStore = "transport_logs"

	// LatencyLogStore is where transport latency logs are stored
	LatencyLogStore = "latency_logs"

	// Custom path to serve files from dmsghttp log server over dmsg
	Custom = "custom"

	// LocalPath where the visor writes files to
	LocalPath = "./local"

	// Default hypervisor constants

	// EnableAuth enables auth on the hypervisor UI
	EnableAuth = false

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

	// GeoIP is the URL of default geoip service to work with IP
	GeoIP string = "http://ip.skycoin.com"
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
