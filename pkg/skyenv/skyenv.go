// Package skyenv pkg/skyenv/skyenv.go c0-com-env
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
	//
	// The skynet mirror reserves this number in the appnet porter at
	// visor init (whenever the visor has hypervisor/dmsgpty-whitelist
	// PKs — i.e. almost always), so it must not collide with ANY
	// skynet port either, not just with another dmsg one. It was 44
	// (VPNServerPort), which made vpn-server's `Listen(skynet, 44)`
	// fail with "port already bound" on every hypervisor-connected
	// board; then 57 (SkyForwardingServerPort), which raced the
	// sky_forward_conn module for the same number — whichever bound
	// second lost, and a lost sky_forward_conn is a FATAL module-init
	// error that takes the whole visor down at boot.
	DmsgVisorRPCPort uint16 = 65

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

	// DmsgDMSGDRegistrationCXOPort is the dmsg port the dmsg-discovery's CXO
	// client-entry REGISTRATION aggregator binds (and each visor's entry
	// publisher binds for the reverse subscribe). A visor publishes its own
	// signed disc.Entry as a CXO feed here and announces to dmsg-disc, which
	// subscribes back and ingests the entry — moving registration off the
	// timer-driven HTTP PUT (each a fresh Noise+PQ handshake) that dominates
	// dmsg-discovery CPU. Distinct from DmsgCXOPort (50, the visor's telemetry
	// publisher) so the entry feed runs on its own CXO node without colliding
	// with the stats listener on the same visor. Numbered 67 (not adjacent to
	// the 50–55 CXO block) because 56 is DmsgWebRTCSignalPort; the value only
	// needs to be collision-free, which the ports_test guards.
	DmsgDMSGDRegistrationCXOPort uint16 = 67

	// DmsgVisorTPListCXOPort is the DMSG port the visor's DEDICATED
	// transport-list (tp-list) discovery feed binds — a second CXO node,
	// under the SAME visor identity PK as the telemetry publisher on
	// DmsgCXOPort (50), carrying ONLY the compact tp-list snapshot leaf.
	// Its Root is a handful of objects, so TPD's aggregator fills it
	// COMPLETELY in ~1 round-trip — where the combined telemetry Root on
	// port 50 (hundreds/thousands of transports/<uuid>/current leaves)
	// routinely breaks its fill partway on a busy hub, landing ~10% of the
	// transports. Same PK keeps TPD's reporter=feed-PK edge-auth intact
	// (see cxo_register.go); a distinct node/port keeps the tiny Root out
	// of the churning telemetry tree AND out of head-collision with it on
	// the aggregator side (each aggregator node sees one Root per feed PK).
	// TPD runs a second aggregator here; older TPDs (port 50 only) still
	// read the tp-list from the combined feed (back-compat fallback).
	// Numbered 69 (the 50–55 CXO block and 56–68 are all taken); the value
	// only needs to be collision-free, which ports_test guards.
	DmsgVisorTPListCXOPort uint16 = 69

	// DmsgTransportQueryPort is the dmsg port a visor listens on to answer
	// RSN-oracle transport-list queries (see pkg/router/transport_query.go). A
	// source visor building a 2-hop route dials the destination here, delivers an
	// RSN-signed TransportQuery, and receives the destination's own transport
	// list. Control-plane only; served solely when Routing.EnableRSNOracleRoutes
	// is set (default OFF), so it adds no listener on the default configuration.
	DmsgTransportQueryPort uint16 = 68

	// DmsgWebRTCSignalPort is the dmsg port the WebRTC carrier exchanges its SDP
	// offer/answer + ICE candidates on (a visor dials a peer here to open a
	// signaling stream; the answerer listens). It MUST live in this registry: it
	// previously hardcoded 47 in pkg/transport/network, colliding with
	// DmsgTransportSetupPort (47) — so the webrtc listener lost the bind and
	// offers reached the transport-setup listener instead, leaving webrtc dead
	// fleet-wide.
	DmsgWebRTCSignalPort uint16 = 56

	// SkychatVoiceSignalPort is the port skychat voice-call SIGNALING listens on
	// — invite / accept / decline / hangup + media-session negotiation. The SAME
	// port number is used over BOTH dmsg AND skynet: the callee listens on it on
	// both networks, and the caller reaches it over whichever is available. Voice
	// MEDIA rides a separately negotiated stream/datagram, not this control port.
	SkychatVoiceSignalPort uint16 = 62

	// SkychatVoiceMediaPort is the default port for the 1:1 voice MEDIA stream
	// (RTP over a skywire transport). Also identical across dmsg + skynet; the
	// exact port is confirmed in the signaling handshake so it can be varied.
	SkychatVoiceMediaPort uint16 = 63

	// SkychatGroupProbePort is the well-known dmsg port a visor answers
	// group DESCRIBE requests on: "what is the group with this ID — a group
	// or a channel, open or approval-gated, on which feed port?".
	//
	// It exists because a group's own admission listener sits at
	// Record.Port+1, where Record.Port is allocated at random per group
	// (see group.defaultPortAlloc) and is therefore unknowable to anyone
	// who does not already hold an invite link. That made the short
	// skychat://<pk>/<group-id> address unusable on its own — the whole
	// point of which is to be short enough to scan or read aloud, which an
	// invite link carrying port, mode, admin list and PoW price is not.
	// One fixed port per visor closes that gap: dial it with a group ID and
	// the answer carries everything a join needs.
	//
	// Describe ONLY. Admission decisions stay on the per-group listener,
	// which the joiner can reach once the probe has told it the port — so
	// this port never mutates state and a flood of probes costs a store
	// read apiece.
	//
	// Deliberately not a small well-known number: 49–64 are fully assigned
	// and the pair/group feed allocators own [10000, 65000). Same reasoning
	// as SkydexMarketPort picking 8050.
	SkychatGroupProbePort uint16 = 8060

	// SkychatFilePort is the port skychat FILE TRANSFER listens on. A sender
	// dials it (over dmsg OR skynet — same port on both, like voice) and streams
	// one offer header + chunked, sha256-verified bytes. Files are conversation
	// messages (Telegram-style): the offer appears in chat/group history and the
	// bytes stream out-of-band on this port so a large transfer never blocks the
	// chat message conn. Peers already paired 1:1 or sharing a group auto-accept.
	SkychatFilePort uint16 = 64

	// DmsgDHTPort Listening port for the Kademlia DHT protocol.
	DmsgDHTPort uint16 = 100

	// DmsgAwaitSetupPort Listening port of a visor for setup operations.
	DmsgAwaitSetupPort uint16 = 136

	// Transport port constants.

	// TransportPort Listening port of a visor for incoming transports.
	TransportPort uint16 = 45

	// PublicAutoconnect determines if the visor automatically creates stcpr transports to public visors
	PublicAutoconnect = true
	// HypervisorAutoconnect determines if the visor automatically creates a direct
	// (stcpr/sudph) transport to each configured hypervisor, so RPC + skypty ride a
	// fast p2p path instead of the dmsg relay (dmsg stays the bootstrap/fallback).
	HypervisorAutoconnect = true

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

	// VPNRouterName is the name of the vpn router (gateway / WiFi-AP) app. It is a
	// LOCAL companion to vpn-client with no dmsg port of its own: it aggregates
	// downstream LAN/WiFi clients and NATs them into the tunnel vpn-client owns.
	VPNRouterName = "vpn-router"

	// VPNRouterPort is a nominal launcher port for the vpn-router app. The app
	// serves no dmsg endpoint (it's a local gateway), but every launcher
	// AppConfig carries a port; this keeps it unique from the others.
	//
	// It was 62 (SkychatVoiceSignalPort), then 58 — which is SkyPingPort,
	// bound on skynet by initPing on EVERY visor, where a failed bind is a
	// fatal module-init error. Nominal or not, an app port has to stay out
	// of the numbers the visor itself claims.
	VPNRouterPort uint16 = 66

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

	// SkyForwardingMuxPort is the yamux-multiplexed variant of the skyfwd server:
	// one accepted route group carries a yamux session, and each stream runs the
	// SAME ready-byte + ClientMsg handshake as SkyForwardingServerPort. This lets a
	// caller hold ONE multihop route open and reuse it across many short
	// connections (the route's keepalive keeps every hop warm), instead of dialing
	// a fresh route per connection — the fix for multihop skynet routes dying under
	// the resolving proxy. Version negotiation is by port availability: a caller
	// dials this port for route reuse and falls back to the 1:1
	// SkyForwardingServerPort against older visors that don't serve it.
	SkyForwardingMuxPort uint16 = 59

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

	// SkydexMarketName is the name of the decentralized-exchange market app.
	SkydexMarketName = "skydex-market"

	// SkydexMarketPort is the market's app routing port. Clients dial the
	// market's public key on this port over dmsg to speak the exchange protocol.
	// 8050 (not a small well-known port) avoids collision with the dmsg service
	// ports (49–56) and existing app ports.
	SkydexMarketPort uint16 = 8050

	// SkydexMarketAddr is the address the market serves its operator UI on
	// (config + monitoring). Port matches SkydexMarketPort for symmetry.
	SkydexMarketAddr = ":8050"

	// SkydexClientName is the name of the decentralized-exchange client app.
	SkydexClientName = "skydex-client"

	// SkydexClientPort is the client's app routing port (source port for its
	// dmsg dials to the market).
	SkydexClientPort uint16 = 8051

	// SkydexClientAddr is the address the client serves its trading UI
	// (single-page app) on. Port matches SkydexClientPort for symmetry.
	SkydexClientAddr = ":8051"

	// RPC constants.

	// RPCAddr for skywire-cli to access skywire-visor. Also hosts
	// the dmsg-bridge protocol (cmux-multiplexed) when the CLI is
	// invoked with `--via dmsg://<pk>`. A connection whose first
	// bytes match dmsgBridgeMagic gets routed to the bridge
	// handler; everything else falls through to gRPC or net/rpc.
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

// AppDisplayName maps an app's id to the human label a user-facing surface
// shows for it — today the app name on a desktop notification.
//
// It lives here, beside the ids themselves, because more than one place needs
// the same answer: the visor's notification hub posting locally, and the
// `hv notify` bridge posting on a different machine. An unknown id passes
// through unchanged, so a new app reads sensibly before it is listed.
func AppDisplayName(app string) string {
	switch app {
	case SkychatName:
		return "Skychat"
	case SkysocksClientName:
		return "Skysocks"
	case VPNClientName:
		return "SkyVPN"
	case SkydexClientName:
		return "SkyDEX"
	case "":
		return "Skywire"
	}
	return app
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
