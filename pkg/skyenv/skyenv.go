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

	// DmsgVisorRPCPort is the dmsg port where a visor exposes its
	// full net/rpc API for remote CLI access. Mirrored on skynet via
	// goServeSkynetMirror. Authentication uses the visor's
	// hypervisor + dmsgpty whitelist (the same gate as
	// TransportRPCServer). Drives `skywire cli ... --rpc dmsg://<pk>`.
	//
	// Distinct from DmsgHypervisorPort (46) — that's the direction
	// visor-dials-hypervisor (visor side serves rpc.Server, hypervisor
	// side runs rpc.Client). Here the visor itself serves rpc.Server
	// to whoever's authorized to dial in.
	DmsgVisorRPCPort uint16 = 44

	// DmsgHypervisorPort Listening port of a hypervisor for incoming RPC visor connections over dmsg.
	DmsgHypervisorPort uint16 = 46

	// DmsgTransportSetupPort Listening port for transport setup RPC over dmsg.
	DmsgTransportSetupPort uint16 = 47

	// DmsgTransportSetupServicePort Listening port for transport setup service requests over dmsg.
	// This is the port where TPS nodes listen for incoming transport setup requests from visors.
	DmsgTransportSetupServicePort uint16 = 48

	// DmsgGRPCPort is the DMSG port for gRPC services (remote gotop, stats, etc.)
	DmsgGRPCPort uint16 = 49

	// DmsgCXOPort is the DMSG port the visor's CXO TreeStore publisher
	// listens on (and the TPD aggregator + other visors dial). Picked
	// outside the 46–49 dmsg-RPC cluster so the CXO listener can't
	// collide with DmsgHypervisorPort.
	DmsgCXOPort uint16 = 50

	// DmsgTPDMetricsCXOPort is the DMSG port the TPD's CXO metrics-
	// aggregate publisher listens on (and visors dial when they want
	// to subscribe to the network-wide transport metrics feed).
	// Distinct from DmsgCXOPort because TPD already runs its
	// CXO aggregator there for inbound visor stats publishers; the
	// metrics publisher is a separate feed in the opposite direction.
	DmsgTPDMetricsCXOPort uint16 = 51

	// DmsgTPDUptimeCXOPort is the DMSG port the TPD's CXO visor-uptime
	// publisher listens on. Mirrors DmsgTPDMetricsCXOPort but for the
	// network-wide visor-uptime feed (timeline + daily-percent shape
	// of `GET /uptimes?v=v3`); kept on a distinct port so a subscriber
	// can pick exactly the feed it wants without dragging in metrics.
	DmsgTPDUptimeCXOPort uint16 = 52

	// DmsgSDServicesCXOPort is the DMSG port the service-discovery's
	// CXO services publisher listens on. Subscribers (the hypervisor's
	// network visualizer + tab-specific consumers) dial this port to
	// receive incremental service register/deregister events at
	// services/<type>/<pk>/{entry,tombstone}.
	DmsgSDServicesCXOPort uint16 = 53

	// DmsgDMSGDClientsByServerCXOPort is the DMSG port the
	// dmsg-discovery's CXO clients-by-server publisher listens on.
	// Subscribers dial it to receive the denormalized clients-by-server
	// index at clients-by-server/<server-pk>/<client-pk>/{entry,tombstone}
	// — the shape the network visualizer's "who's on each dmsg server"
	// view consumes. Distinct port from DmsgSDServicesCXOPort so
	// consumers can subscribe to one feed without the other.
	DmsgDMSGDClientsByServerCXOPort uint16 = 54

	// DmsgTPDAllTransportsCXOPort is the DMSG port the TPD's CXO
	// all-transports snapshot publisher listens on. Mirrors the
	// HTTP /all-transports endpoint on a fixed cadence; subscribers
	// (the CLI's `pv -t`, `tp tree`, `tp viz`) read the snapshot
	// instead of HTTP-polling. Distinct port from
	// DmsgTPDMetricsCXOPort and DmsgTPDUptimeCXOPort so consumers can
	// subscribe to one TPD feed without dragging in the others.
	DmsgTPDAllTransportsCXOPort uint16 = 55

	// DmsgDHTPort Listening port for the Kademlia DHT protocol.
	DmsgDHTPort uint16 = 100

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

	// SkymailBridgeAddr is the local TCP address the in-process
	// skymail-bridge listens on for inbound SMTP from a co-located
	// Postfix's transport_map. The non-default :1025 (vs :25) keeps
	// the bridge from racing the host's primary mailserver on the
	// privileged port. Lives here rather than under a config field
	// default so the config-gen flag (`--skymail-bridge`) emits a
	// concrete value, not a "leave empty for default" sentinel.
	SkymailBridgeAddr = "127.0.0.1:1025"

	// SkychatName is the name of the skychat app
	SkychatName = "skychat"

	// SkychatPort is the dmsg port used by skychat
	SkychatPort uint16 = 1

	// SkychatAddr is the non-dmsg address skychat binds for its HTTP
	// UI. Localhost-only by default since skychat is unauthenticated
	// out of the box; the operator can opt into wider exposure with
	// "*:8001" (the docker integration configs do this for inter-
	// container reachability), and optional password protection is
	// available via the hypervisor's Skychat password setting.
	SkychatAddr = "127.0.0.1:8001"

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

	// SkycoinDaemonName is the name of the embedded skycoin daemon
	// app (`skywire skycoin daemon`). Default-off in config-gen
	// (AutoStart=false) — the daemon syncs the chain to ~/.skycoin
	// and is heavy enough that we don't want it firing on every
	// visor that doesn't ask for it.
	SkycoinDaemonName = "skycoin-daemon"
	// SkycoinDaemonPort is unused over Skywire routes (the daemon
	// serves HTTP on localhost:6420 by default), but the launcher
	// requires a non-zero routing port for app accounting; pick a
	// value outside the in-use cluster.
	SkycoinDaemonPort uint16 = 60

	// SkycoinWebName is the name of the embedded skycoin thin-client
	// web wallet app (`skywire skycoin web`). Default-off; intended
	// to run as the operator's own user (set AppConfig.User) so the
	// wallet directory under ~/.skycoin/wallets stays writable.
	SkycoinWebName = "skycoin-web"
	// SkycoinWebPort — same routing-port note as SkycoinDaemonPort.
	SkycoinWebPort uint16 = 61

	// RPC constants.

	// RPCAddr for skywire-cli to access skywire-visor
	RPCAddr = "localhost:3435"

	// DmsgBridgeAddr is where the visor exposes a TCP bridge for
	// the CLI's `--rpc dmsg://<pk>` path. The CLI dials this
	// address, sends a 35-byte header (33 PK bytes + 2 port bytes),
	// and the visor proxies bytes between the TCP conn and a dmsg
	// stream it opens to the target. The CLI then runs its rpc.Client
	// on the TCP conn, talking to the remote visor's rpc.Server
	// transparently. Lets the CLI inherit the visor's dmsg identity
	// (and thus the visor's whitelist eligibility on remote peers)
	// without needing a separate CLI keypair.
	DmsgBridgeAddr = "localhost:3437"

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

	// TpLogStore is the legacy on-disk transport-log directory name.
	// Retained because cmd/skywire-cli/commands/config/gen.go still
	// emits it as the default LogStore.Location for backward-compatible
	// config files; the runtime no longer writes anything there
	// (historical bandwidth lives in the bbolt stats store).
	TpLogStore = "transport_logs"

	// LocalPath where the visor writes files to
	LocalPath = "./local"

	// Default hypervisor constants

	// EnableAuth enables auth on the hypervisor UI
	EnableAuth = true

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
