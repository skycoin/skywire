// Package visorconfig pkg/visor/visorconfig/v1.go
package visorconfig

import (
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsgspec "github.com/skycoin/skywire/pkg/dmsgc/spec"
	tnspec "github.com/skycoin/skywire/pkg/transport/network/spec"
	tspec "github.com/skycoin/skywire/pkg/transport/spec"
)

// V1 is visor config
type V1 struct {
	*Common
	mu sync.RWMutex

	Dmsg          *dmsgspec.DmsgConfig `json:"dmsg"`
	Pty           *Pty                 `json:"pty,omitempty"`
	Dmsgscp       *Dmsgscp             `json:"dmsgscp,omitempty"`
	UIServer      *UIServer            `json:"ui_server,omitempty"`
	LogServer     *LogServer           `json:"log_server,omitempty"`
	DmsgWeb       *DmsgWebConfig       `json:"dmsg_web,omitempty"`
	SkynetWeb     *SkynetWebConfig     `json:"skynet_web,omitempty"`
	SkymailBridge *SkymailBridgeConfig `json:"skymail_bridge,omitempty"`
	Rewards       *RewardsConfig       `json:"rewards,omitempty"`
	STCP          *tnspec.STCPConfig   `json:"skywire-tcp,omitempty"`
	Transport     *Transport           `json:"transport"`
	Routing       *Routing             `json:"routing"`
	UptimeTracker *UptimeTracker       `json:"uptime_tracker,omitempty"`
	Launcher      *Launcher            `json:"launcher"`

	// Stats configures the visor-local telemetry store. Nil/zero
	// values use defaults; Disabled=true skips the store entirely.
	Stats *Stats `json:"stats,omitempty"`

	// Skychat carries chat-app-related visor-side configuration that
	// doesn't fit elsewhere. Currently just opt-in group-message
	// persistence; expected to grow as the chat surface evolves.
	Skychat *Skychat `json:"skychat,omitempty"`

	SurveyWhitelist     []cipher.PubKey `json:"survey_whitelist"`
	UserSurveyWhitelist []cipher.PubKey `json:"user_survey_whitelist,omitempty"` // user-added keys, preserved across config refresh
	Hypervisors         []cipher.PubKey `json:"hypervisors"`
	CLIAddr             string          `json:"cli_addr"`

	LogLevel             string                       `json:"log_level"`
	LocalPath            string                       `json:"local_path"`
	StunServers          []string                     `json:"stun_servers"`
	ShutdownTimeout      Duration                     `json:"shutdown_timeout,omitempty"` // time value, examples: 10s, 1m, etc
	IsPublic             bool                         `json:"is_public"`
	PublicVisorConfig    *PublicVisorConfig           `json:"public_visor,omitempty"`
	GeoIP                string                       `json:"geoip"`
	PersistentTransports []tspec.PersistentTransports `json:"persistent_transports"`
	ConfService          string                       `json:"conf_service,omitempty"`      // HTTP URL for config bootstrap service
	ConfServiceDmsg      string                       `json:"conf_service_dmsg,omitempty"` // DMSG URL for config bootstrap service
	// SurveyClientSK is the CLI-side identity used by
	// `skywire cli log <subcommand>` to authenticate against remote
	// visors' survey_whitelist gates (visor logserver port 80 over
	// dmsg). When non-empty, parses as a hex SecKey; the derived PK
	// is the identity the remote checks against its survey_whitelist.
	// Empty (default) falls through to --sk flag → DMSGCURL_SK env →
	// ephemeral random keypair. Server-side: never read; the visor
	// ignores this field entirely.
	SurveyClientSK string `json:"survey_client_sk,omitempty"`

	RewardAddress    string `json:"reward_address,omitempty"`
	RewardSystem     string `json:"reward_system,omitempty"`
	RewardSystemDmsg string `json:"reward_system_dmsg,omitempty"`
	MemoryLimit      string `json:"memory_limit,omitempty"` // Go memory limit (e.g., "256MiB", "auto" for 60% of available RAM)

	Hypervisor *HypervisorConfig `json:"hypervisor,omitempty"`
}

// Skychat carries chat-app-related visor-side configuration. Optional —
// nil disables all knobs (the visor still runs chat groups in-memory).
type Skychat struct {
	// GroupHistoryDB, when non-empty, enables persistent group-message
	// history at the visor layer. Each inbound group message is mirrored
	// into a bolt store at this path. The Visor.GroupHistory RPC (and
	// `skywire cli skychat group history`) read from the same store.
	//
	// Path may be absolute (used verbatim) or relative — relative paths
	// resolve under <LocalPath>/skychat/. Empty (default) disables
	// persistence: groups remain in-memory-only, bounded by the inbox
	// ring buffer (~1024 messages).
	//
	// Limits track history.DefaultLimits() with a couple of group-
	// aware overrides (rate-limit disabled, larger per-group cap).
	// A future config knob may expose the limits directly.
	GroupHistoryDB string `json:"group_history_db,omitempty"`
}

// Pty configures the pseudoterminal subsystem (formerly Dmsgpty).
// The struct shape is unchanged across the rename; the V1.Pty
// field's JSON tag accepts both "pty" (canonical) and "dmsgpty"
// (legacy) via V1.UnmarshalJSON in config_compat.go.
type Pty struct {
	DmsgPort  uint16          `json:"dmsg_port"`
	CLINet    string          `json:"cli_network"`
	CLIAddr   string          `json:"cli_address"`
	Whitelist []cipher.PubKey `json:"whitelist"`

	// SshListen, when non-empty, brings up a direct-TCP entry point
	// for the dmsgpty / dmsgscp / dmsgcat protocols, separate from the
	// dmsg-overlay path. Listening address in net.Listen format —
	// `:2022`, `0.0.0.0:2022`, `127.0.0.1:2022`, etc. Empty (default)
	// disables it: only the dmsg-overlay entry point is served.
	//
	// This is the surface that `skywire cli ssh` (client) and
	// `skywire cli sshd` (server) expose as the OpenSSH-equivalent
	// shell over skywire identity.
	//
	// Auth flow per accepted TCP connection:
	//   1. Noise XK handshake using the visor's identity keypair —
	//      the client side pins the server's PK (same trust model as
	//      ssh's host key check) and the server learns the client's PK
	//      from the handshake.
	//   2. The learned client PK is checked against the SAME whitelist
	//      that gates the dmsg-overlay accept loop. No separate ACL.
	//   3. On accept, the stream is handed to the existing dmsgpty
	//      mux so dmsgpty / dmsgscp / dmsgcat clients work uniformly
	//      over either transport.
	//
	// Motivation: operators who have direct IP reachability to a peer
	// can `cli ssh <pk>@<host>:<port>` — no dmsg-discovery dependency,
	// lower latency for known endpoints, same PK-based auth model.
	SshListen string `json:"ssh_listen,omitempty"`
}

// Dmsgscp configures the dmsgscp-host (scp-over-dmsg daemon).
// The host is on by default — access is gated by the same whitelist
// that dmsgpty uses (a peer's PK must be on the list to be served).
// Operators who want to disable it entirely set Disabled=true. When
// Whitelist is empty the host reuses Pty.Whitelist so trusting
// a peer for pty implicitly trusts them for file transfer too.
//
// The host listens on BOTH dmsg and the skywire router (skynet) at
// the same port number — same dual-listen pattern dmsg_http and
// others use. dmsg covers the bootstrap path (works before any
// router transport is up); skynet covers steady-state operation
// over arbitrary transports.
type Dmsgscp struct {
	// Disabled, when true, suppresses both the dmsg and skynet
	// listeners. Default false (== on). Inverted from the original
	// `Enabled` field so the zero value matches the default.
	Disabled bool `json:"disabled,omitempty"`
	// DmsgPort is the dmsg port to listen on; zero means use
	// dmsgscp.DefaultPort (23). The skynet mirror binds the same
	// port number on the router for parity.
	DmsgPort uint16 `json:"dmsg_port,omitempty"`
	// RootDir is the filesystem directory the host serves files
	// from (its effective chroot). Empty means
	// <local_path>/scp-root is used.
	RootDir string `json:"root_dir,omitempty"`
	// Whitelist optionally overrides the dmsgpty whitelist. When
	// nil or empty, init reuses Pty.Whitelist.
	Whitelist []cipher.PubKey `json:"whitelist,omitempty"`
}

// UIServer configures the visor UI server (serves tp-viz and other UIs).
type UIServer struct {
	Enable        bool            `json:"enable"`         // Enable the UI server
	LocalAddr     string          `json:"local_addr"`     // Local HTTP address (default: localhost:8081)
	DmsgPort      uint16          `json:"dmsg_port"`      // DMSG port to serve on (default: 81, 0 to disable)
	DmsgWhitelist []cipher.PubKey `json:"dmsg_whitelist"` // Keys allowed to access via DMSG
	SurveyDir     string          `json:"survey_dir"`     // Directory with visor surveys for IP-based grouping
}

// Stats configures the visor-local telemetry store. Defaults are
// applied lazily when the field is nil or any individual sub-field
// is its zero value, so dropping the entire block from a config is
// safe.
type Stats struct {
	// Path is the absolute path to the bbolt file. Empty = "<local_path>/stats.db".
	Path string `json:"path,omitempty"`
	// SampleInterval is the sampler tick (e.g. "1m"). Empty = 1m.
	SampleInterval Duration `json:"sample_interval,omitempty"`
	// RetentionDays caps bbolt history. 0 = 30 days.
	RetentionDays int `json:"retention_days,omitempty"`
	// CXOPublishWindow caps the rolling window the CXO publisher
	// exposes (in days). 0 = 7. Must be ≤ RetentionDays.
	CXOPublishWindow int `json:"cxo_publish_window,omitempty"`
	// Disabled, when true, skips the entire telemetry store and
	// associated /stats/* endpoints + CXO publisher.
	Disabled bool `json:"disabled,omitempty"`
}

// LogServer configures the dmsghttp log server's optional localhost endpoint.
// When LocalAddr is set (non-empty), the log server also serves on localhost without authentication.
type LogServer struct {
	// LocalAddr enables serving on localhost (e.g., "localhost:8002" or "127.0.0.1:8002").
	// If empty, localhost serving is disabled (dmsg-only mode).
	LocalAddr string `json:"local_addr"`
}

// RewardsConfig configures the reward system UI when hosted by the visor.
type RewardsConfig struct {
	Enable          bool            `json:"enable"`
	WorkDir         string          `json:"work_dir"`
	Whitelist       []cipher.PubKey `json:"whitelist,omitempty"`
	CanonicalDomain string          `json:"canonical_domain,omitempty"`
	SkycoinNode     string          `json:"skycoin_node,omitempty"`
	LoginNode       string          `json:"login_node,omitempty"`
}

// DmsgWebConfig enables the embedded `.dmsg` resolving proxy hosted by
// the visor. When Enable is true, the visor serves a SOCKS5 proxy on
// ProxyPort plus an HTTP bridge on WebPort; browsers pointed at the
// SOCKS5 proxy can load http://somekey.dmsg URLs directly. Reuses the
// visor's own dmsg client for the outbound fetches, so no extra dmsg
// identity or session load is incurred.
//
// This is the in-process version of the `skywire dmsg web` CLI, so
// the same domain-suffix resolving behavior is available without
// running a separate utility alongside the visor.
type DmsgWebConfig struct {
	// Enable must be true for the resolver to start.
	Enable bool `json:"enable"`
	// ProxyPort is the local SOCKS5 listener. Default 4445.
	ProxyPort uint `json:"proxy_port,omitempty"`
	// WebPort is the local HTTP bridge the SOCKS5 proxy rewrites
	// matched hosts to. Default 8080.
	WebPort uint `json:"web_port,omitempty"`
	// DomainSuffix is the TLD treated as DMSG addresses. Default ".dmsg".
	DomainSuffix string `json:"domain_suffix,omitempty"`
	// UpstreamSOCKS, if set, forwards non-matching CONNECTs to this
	// upstream SOCKS5 server (e.g. "127.0.0.1:1080" for a chained
	// skysocks-client). Empty = direct connect.
	UpstreamSOCKS string `json:"upstream_socks,omitempty"`
	// TLSMITM enables on-the-fly TLS termination for browser
	// connections to <pk>.dmsg on TLSPort, using a leaf cert minted
	// from a locally-installed CA at TLSCAPath/TLSCAKeyPath. Off by
	// default; when off the resolver behaves exactly as before.
	// See pkg/skynetca and docs/skynet-tls.md for the trust model.
	TLSMITM bool `json:"tls_mitm,omitempty"`
	// TLSPort is the destination port treated as TLS. Default 443.
	TLSPort uint16 `json:"tls_port,omitempty"`
	// TLSCAPath / TLSCAKeyPath point to a CA generated by
	// `skywire cli resolver ca gen`. Both required when TLSMITM
	// is true. The visor logs and falls back to non-MITM mode if
	// either is missing or unreadable; it does not refuse to start.
	TLSCAPath    string `json:"tls_ca_path,omitempty"`
	TLSCAKeyPath string `json:"tls_ca_key_path,omitempty"`
}

// SkynetWebConfig enables the embedded `.skynet` resolving proxy — the
// skywire-routing counterpart to DmsgWebConfig. Browsers pointed at
// the SOCKS5 listener can load http://<pk>.skynet[:<port>] URLs,
// which the visor serves by dialing a route to the remote visor's
// skynet server and piping bytes through. Reuses the visor's own
// router, so no additional routing setup is needed.
//
// Ports default to non-conflicting values with DmsgWebConfig so both
// resolvers can run simultaneously on a single visor.
type SkynetWebConfig struct {
	// Enable must be true for the resolver to start.
	Enable bool `json:"enable"`
	// ProxyPort is the local SOCKS5 listener. Default 4446.
	ProxyPort uint `json:"proxy_port,omitempty"`
	// WebPort is the local HTTP bridge port. Default 8081.
	WebPort uint `json:"web_port,omitempty"`
	// DomainSuffix is the TLD treated as skynet addresses. Default ".skynet".
	DomainSuffix string `json:"domain_suffix,omitempty"`
	// UpstreamSOCKS forwards non-matching CONNECTs to this upstream.
	UpstreamSOCKS string `json:"upstream_socks,omitempty"`
	// RouteTimeout is the keepalive duration for routes created by the
	// resolving proxy. Routes idle longer than this are expired by GC.
	// Zero means use DefaultRouteKeepAlive (10 min). A very large value
	// (e.g. "8760h") effectively keeps routes alive until the visor stops.
	RouteTimeout Duration `json:"route_timeout,omitempty"`
	// TLSMITM enables on-the-fly TLS termination for browser
	// connections to <pk>.skynet on TLSPort, using a leaf cert minted
	// from a locally-installed CA at TLSCAPath/TLSCAKeyPath. Off by
	// default; when off the resolver behaves exactly as before.
	// See pkg/skynetca and docs/skynet-tls.md for the trust model.
	TLSMITM bool `json:"tls_mitm,omitempty"`
	// TLSPort is the destination port treated as TLS. Default 443.
	TLSPort uint16 `json:"tls_port,omitempty"`
	// TLSCAPath / TLSCAKeyPath point to a CA generated by
	// `skywire cli resolver ca gen`. Both required when TLSMITM
	// is true. The visor logs and falls back to non-MITM mode if
	// either is missing or unreadable; it does not refuse to start.
	TLSCAPath    string `json:"tls_ca_path,omitempty"`
	TLSCAKeyPath string `json:"tls_ca_key_path,omitempty"`
}

// SkymailBridgeConfig configures the embedded SMTP-aware bridge that
// relays recipient envelopes of either shape —
// user@<host>.<base32-pk>.skynet or user@<base32-pk>.skynet — to a
// peer visor's exposed SMTP listener over the visor's existing
// dmsg session.
//
// Structurally parallel to DmsgWebConfig — runs inside the visor
// process, lifecycled via RPC (SetEmbeddedProxyEnabled("bridge", …)),
// no separate launcher app needed. Operators who don't run a full
// visor can use the standalone binary at cmd/smb instead.
type SkymailBridgeConfig struct {
	// Enable must be true for the bridge to start.
	Enable bool `json:"enable"`
	// Addr is the local TCP listener Postfix's transport_map relays
	// to (e.g. "127.0.0.1:1025"). Default "127.0.0.1:1025".
	Addr string `json:"addr,omitempty"`
	// Mode selects the RCPT TO rewrite rule. Both modes accept both
	// address shapes (host-prefixed and bare-PK); they differ only
	// in what mode b does with the host-prefixed shape.
	//
	//   "b" (default) — strip-when-prefixed: ".<pk><suffix>" is
	//                   stripped from host-prefixed envelopes before
	//                   forwarding (so the receiver's existing
	//                   virtual_alias chain handles delivery
	//                   natively); bare-PK envelopes are forwarded
	//                   verbatim. Strict superset of mode a.
	//   "a"           — verbatim: forward both shapes unchanged.
	//                   Receiver's Postfix must accept the full
	//                   ".<pk><suffix>" domain in every envelope.
	//
	// Either way, support for the bare-PK shape requires the
	// receiver's Postfix to know the bare-PK domain — via
	// mydestination or virtual_alias_domains.
	Mode string `json:"mode,omitempty"`
	// Suffix is the TLD the bridge treats as a skywire-routed
	// recipient. Defaults to ".skynet". Independent of the resolver's
	// DomainSuffix — operators may serve mail under a different TLD
	// than the resolver if they have reason to.
	Suffix string `json:"suffix,omitempty"`
	// HeloName is the EHLO/HELO greeting the bridge sends to the
	// peer's Postfix. Mostly cosmetic (appears in Received: trace
	// headers). Default "skymail-bridge.local".
	HeloName string `json:"helo_name,omitempty"`
	// RemotePort is the dmsg routing port to dial on the peer.
	// Default 25 (matching the SMTP convention). Receiver must
	// expose Postfix's smtpd on this port via
	// `skywire cli serve add 25 --to 127.0.0.1:25`.
	RemotePort uint16 `json:"remote_port,omitempty"`
}

// Transport defines a transport config.
type Transport struct {
	Discovery             string          `json:"discovery"`
	DiscoveryDmsg         string          `json:"discovery_dmsg,omitempty"` // DMSG-HTTP URL for transport discovery (fallback pair with discovery)
	AddressResolver       string          `json:"address_resolver"`
	AddressResolverDmsg   string          `json:"address_resolver_dmsg,omitempty"` // DMSG-HTTP URL for address resolver
	PublicAutoconnect     bool            `json:"public_autoconnect"`
	TransportSetupPKs     []cipher.PubKey `json:"transport_setup"`
	UserTransportSetupPKs []cipher.PubKey `json:"user_transport_setup,omitempty"` // user-added keys, preserved across config refresh
	TPSetupSK             *cipher.SecKey  `json:"tps_sk,omitempty"`
	TPSDmsg               *TPSDmsgConfig  `json:"tps_dmsg,omitempty"`
	LogStore              *LogStore       `json:"log_store"`
	StcprPort             int             `json:"stcpr_port"`
	SudphPort             int             `json:"sudph_port"`
	// ARTransportLimit controls address resolver registration for privacy:
	//   0 (default): stay registered indefinitely
	//   N > 0: deregister from AR after N transports are established
	//   N < 0: never register with AR at all
	// When deregistered, the visor cannot receive new inbound transports
	// but can still initiate outbound connections.
	ARTransportLimit int `json:"ar_transport_limit,omitempty"`
}

// TPSDmsgConfig configures the embedded Transport Setup Node's dmsg client.
// If nil, defaults are used: MinSessions=0 (connect to all servers), ServerType="" (all types).
type TPSDmsgConfig struct {
	// MinSessions is the minimum number of dmsg server sessions.
	// 0 means connect to ALL available servers (recommended for TPS).
	MinSessions int `json:"min_sessions"`
	// ServerType filters which dmsg servers to connect to: "official", "community", or "" for all.
	ServerType string `json:"server_type"`
}

// LogStore configures a LogStore.
type LogStore struct {
	// Type defines the log store type. Valid values: file, memory.
	Type             string   `json:"type"`
	Location         string   `json:"location"`
	RotationInterval Duration `json:"rotation_interval"` // time value, examples: 10s, 1m, 1h etc
}

// Routing configures routing.
type Routing struct {
	RouteSetupNodes     []cipher.PubKey `json:"route_setup_nodes,omitempty"`
	UserRouteSetupNodes []cipher.PubKey `json:"user_route_setup_nodes,omitempty"` // user-added keys, preserved across config refresh
	RouteSetupSK        *cipher.SecKey  `json:"route_setup_sk,omitempty"`         // Embedded route setup-node secret key
	RouteFinder         string          `json:"route_finder"`
	RouteFinderDmsg     string          `json:"route_finder_dmsg,omitempty"`
	RouteFinderTimeout  Duration        `json:"route_finder_timeout,omitempty"`
	MinHops             uint16          `json:"min_hops"`
	// CalculateRoutes enables local route calculation instead of using the route finder service.
	// When enabled, routes are calculated locally using cached TPD data.
	// Can be overridden at runtime with --use-rf flag.
	CalculateRoutes bool `json:"calculate_routes,omitempty"`
	// MuxRoutes is the number of parallel routes to establish per connection.
	// 0 or 1 = single route (default), >1 = route multiplexing across transports.
	MuxRoutes int `json:"mux_routes,omitempty"`
	// TransportPreference is the priority order applied when multiple transports
	// exist between the same edges. Earlier entries are preferred. Valid values:
	// "stcpr", "sudph", "stcp", "dmsg". If empty, the built-in default order
	// (stcpr > sudph > stcp > dmsg) is used. Types not listed here sort last.
	TransportPreference []string `json:"transport_preference,omitempty"`

	// PolicyPerDial is an optional operator-supplied routing
	// policy script (Starlark, RFC #2882). Accepts either:
	//   "@/path/to/policy.star"  — file-backed, hot-reloaded
	//   "<inline script source>" — small inline policies
	//   "" / unset                — no policy, built-in defaults
	//
	// The script's decide_route(ctx, candidates) function is
	// called per dial; its returned RouteSpec adjusts MuxRoutes
	// and MinHops, and can refuse the dial via fallback="drop".
	//
	// See docs/routing_policy_rfc.md for the policy DSL design,
	// docs/examples/routing-policies/ for example scripts, and
	// `skywire cli route policy test/bench` for iteration tools.
	PolicyPerDial string `json:"policy_per_dial,omitempty"`
}

// UptimeTracker configures uptime tracker.
type UptimeTracker struct {
	Addr     string `json:"addr"`
	AddrDmsg string `json:"addr_dmsg,omitempty"`
}

// PublicVisorConfig configures public visor behavior and service discovery registration.
type PublicVisorConfig struct {
	// RegistrationTimeout is how long to wait for an external STCPR connection
	// before deregistering from service discovery. This validates the visor is
	// actually reachable from the internet (has port forwarding configured).
	// Set to 0 to skip this check (stay registered regardless).
	// Default: 10m
	RegistrationTimeout Duration `json:"registration_timeout,omitempty"`

	// MaxTransports is the maximum transport count before deregistering from
	// service discovery. Once reached, the visor has served its purpose of
	// bootstrapping other visors and deregisters to make room for others.
	// Set to 0 to never deregister based on transport count.
	// Default: 1000
	MaxTransports int `json:"max_transports,omitempty"`
}

// Launcher configures the app
type Launcher struct {
	ServiceDisc     string `json:"service_discovery"`
	ServiceDiscDmsg string `json:"service_discovery_dmsg,omitempty"` // DMSG-HTTP URL for service discovery
	// Apps uses the appsList wrapper so on-disk JSON renders each
	// AppConfig's Args as a single shell-like string (much easier
	// to hand-edit than the array form). Old configs with array-
	// form args still load. Behaviorally this is a []appserver.AppConfig
	// — readers that take the Apps field unchanged still work.
	Apps              appsList `json:"apps"`
	ServerAddr        string   `json:"server_addr"`
	BinPath           string   `json:"bin_path"`
	DisplayNodeIP     bool     `json:"display_node_ip"`
	HeartbeatInterval Duration `json:"heartbeat_interval,omitempty"`
}

// Flush flushes the config to file (if specified).
func (v1 *V1) Flush() error {
	v1.mu.Lock()
	defer v1.mu.Unlock()

	return v1.Common.flush(v1)
}

// UpdateMinHops updates min_hops config
func (v1 *V1) UpdateMinHops(hops uint16) error {
	v1.mu.Lock()
	v1.Routing.MinHops = hops
	v1.mu.Unlock()

	return v1.flush(v1)
}

// UpdatePersistentTransports updates persistent_transports in config
func (v1 *V1) UpdatePersistentTransports(pTps []tspec.PersistentTransports) error {
	v1.mu.Lock()
	v1.PersistentTransports = pTps
	v1.mu.Unlock()

	return v1.flush(v1)
}

// GetPersistentTransports gets persistent_transports from config
func (v1 *V1) GetPersistentTransports() ([]tspec.PersistentTransports, error) {
	v1.mu.Lock()
	defer v1.mu.Unlock()
	return v1.PersistentTransports, nil
}

// UpdateLogRotationInterval updates log_rotation_interval in config
func (v1 *V1) UpdateLogRotationInterval(d Duration) error {
	v1.mu.Lock()
	v1.Transport.LogStore.RotationInterval = d
	v1.mu.Unlock()

	return v1.flush(v1)
}

// GetLogRotationInterval gets log_rotation_interval from config
func (v1 *V1) GetLogRotationInterval() (Duration, error) {
	v1.mu.Lock()
	defer v1.mu.Unlock()
	return v1.Transport.LogStore.RotationInterval, nil
}

// UpdatePublicAutoconnect updates public_autoconnect in config
func (v1 *V1) UpdatePublicAutoconnect(pAc bool) error {
	v1.mu.Lock()
	v1.Transport.PublicAutoconnect = pAc
	v1.mu.Unlock()

	return v1.flush(v1)
}

// UpdateCalculateRoutes updates calculate_routes in routing config
func (v1 *V1) UpdateCalculateRoutes(enabled bool) error {
	v1.mu.Lock()
	v1.Routing.CalculateRoutes = enabled
	v1.mu.Unlock()

	return v1.flush(v1)
}

// GetCalculateRoutes gets calculate_routes from routing config
func (v1 *V1) GetCalculateRoutes() bool {
	v1.mu.RLock()
	defer v1.mu.RUnlock()
	return v1.Routing.CalculateRoutes
}

// mergePubKeys returns the union of the given public key slices, preserving
// the order of the first slice and appending any unique keys from the second.
// nil slices are handled transparently.
func mergePubKeys(primary, extra []cipher.PubKey) []cipher.PubKey {
	if len(extra) == 0 {
		return primary
	}
	if len(primary) == 0 {
		return extra
	}
	seen := make(map[cipher.PubKey]struct{}, len(primary)+len(extra))
	merged := make([]cipher.PubKey, 0, len(primary)+len(extra))
	for _, k := range primary {
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		merged = append(merged, k)
	}
	for _, k := range extra {
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		merged = append(merged, k)
	}
	return merged
}

// EffectiveRouteSetupNodes returns the union of deployment route setup nodes
// (managed by config refresh) and user-added route setup nodes.
func (v1 *V1) EffectiveRouteSetupNodes() []cipher.PubKey {
	if v1 == nil || v1.Routing == nil {
		return nil
	}
	return mergePubKeys(v1.Routing.RouteSetupNodes, v1.Routing.UserRouteSetupNodes)
}

// EffectiveTransportSetupPKs returns the union of deployment transport setup
// public keys (managed by config refresh) and user-added keys.
func (v1 *V1) EffectiveTransportSetupPKs() []cipher.PubKey {
	if v1 == nil || v1.Transport == nil {
		return nil
	}
	return mergePubKeys(v1.Transport.TransportSetupPKs, v1.Transport.UserTransportSetupPKs)
}

// EffectiveSurveyWhitelist returns the union of deployment survey whitelist
// keys (managed by config refresh) and user-added keys.
func (v1 *V1) EffectiveSurveyWhitelist() []cipher.PubKey {
	if v1 == nil {
		return nil
	}
	return mergePubKeys(v1.SurveyWhitelist, v1.UserSurveyWhitelist)
}

/*
// V100Name is the semantic version string for v1.0.0.
const V100Name = "v1.0.0"

// V101Name is the semantic version string for v1.0.1.
const V101Name = "v1.0.1"

// V110Name is the semantic version string for v1.1.0.
// Added MinHops field to V1Routing section of config
// Removed public_trusted_visor field from root section
// Removed trusted_visors field from transport section
// Added is_public field to root section
// Added public_autoconnect field to transport section
// Added transport_setup_nodes field to transport section
// Removed authorization_file field from dmsgpty section
// Default urls are changed to newer shortened ones
// Added stun_servers field to the config
// Added persistent_transports field to the config
// Changed proxy_discovery_addr field to service_discovery
// Changed V1AppDisc struct to V1ServiceDisc
// Changed stcp field to skywire-tcp
// Changed local_address field to listening_address
// Changed port field in dmsgpty to dmsg_port
// Added dmsghttp_path field to the config
const V110Name = "v1.1.0"

// V111Name is the semantic version string for v1.1.1.
// Added support for dmsghttp
// Added servers field in dmsg for dmsghttp
const V111Name = "v1.1.1"

// V1Name is the semantic version string for the most recent version of V1.
const V1Name = V111Name

//(0pcom)
//Version the config using the version of the program.
//Remove previous version parsing compatibility - visor no longer updates it's own config
// Config will be updated on new version via script provided with the installation
*/
