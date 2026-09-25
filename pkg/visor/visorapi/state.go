// Package visorapi pkg/visor/visorapi/state.go c3-vis-core
package visorapi

import (
	"time"

	"github.com/google/uuid"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// StateSnapshot is a curated, secrets-free view of the visor's live runtime
// state, aggregated from the same RPC-safe DTOs the individual CLI subcommands
// return. It exists so an operator (or an agent) can introspect the whole
// runtime in ONE call — `skywire cli visor state` — and project exactly the
// field they want with the shared --jq / --shape flags, instead of stitching
// together a dozen subcommands.
//
// SAFETY: every embedded field is an existing API response type, i.e. already
// designed to cross the RPC boundary — so this carries no mutexes, channels,
// contexts, live connections, or secret keys. The visor's secret key is NEVER
// included; identity is public-key only (via Summary.Overview.PubKey). Each
// section is populated best-effort: a section that errors (e.g. a subsystem not
// yet initialized, or the visor suspended) is left nil and the reason recorded
// in Notes, so a partially-up or suspended visor still returns a useful snapshot
// rather than failing the whole call.
type StateSnapshot struct {
	At        time.Time `json:"at"`
	Suspended bool      `json:"suspended"`

	Summary       *Summary                         `json:"summary,omitempty"`
	Health        *HealthInfo                      `json:"health,omitempty"`
	ServiceHealth []ServiceHealthEntry             `json:"service_health,omitempty"`
	RoutingStats  *routing.RoutingTableStats       `json:"routing_stats,omitempty"`
	RouteGroups   int                              `json:"route_groups"`
	RoutingPolicy *RoutingPoliciesSummary          `json:"routing_policy,omitempty"`
	Apps          []*appserver.AppState            `json:"apps,omitempty"`
	Transports    []*TransportSummary              `json:"transports,omitempty"`
	Persistent    []transport.PersistentTransports `json:"persistent_transports,omitempty"`
	Modules       *ModulePresence                  `json:"modules,omitempty"`

	// RouterConfig is the routing configuration actually IN FORCE at
	// runtime (min_hops / mux_routes / force_local / existing_tp_only
	// / transport_preference are the live router values, which can
	// differ from the config file after a runtime set; cascade +
	// policy_per_dial are the configured source). This is the
	// policy-vs-globals view — what the router will do independent of
	// any per-app routing policy.
	RouterConfig *EffectiveRoutingConfig `json:"router_config,omitempty"`
	// MuxRouteGroups is the per-leg mux shape of EVERY active route
	// group (the same RouteGroupMuxInfo 'mux plot' reads), so the live
	// multipath layout — each leg's transport, type, remote, rtt,
	// bandwidth, and alive/standby gate state — is visible in one
	// snapshot rather than one-app-at-a-time.
	MuxRouteGroups []MuxRouteGroupInfo `json:"mux_route_groups,omitempty"`

	// MuxCounters are the whole-router cumulative tallies (tunnel
	// promotions/flips, leg re-homes sent/received/acked/failed, forward
	// fan-out engage/release) that the bounded mux event ring does not keep
	// once it wraps. Built alongside MuxRouteGroups (--select mux).
	MuxCounters *router.MuxCounters `json:"mux_counters,omitempty"`

	// Pool is the standby-tunnel pool table: one row per route group this
	// visor's local end labeled "standby" (pool_arbiter.go), projected from
	// the SAME MuxRouteGroupInfo entries MuxRouteGroups holds — local port,
	// first-hop pk+transport type, hop count, capacity prior bps, audition
	// age, leg source, and leg count, at a glance across the whole pool.
	Pool []PoolTunnelInfo `json:"pool,omitempty"`

	// CXOFeeds is the live publish-health of each CXO feed (system
	// telemetry/tp-list feed first, then user feeds): dirty state, secs
	// since last OK publish, in-memory leaf/node counts, and any standing
	// publish error with its concrete type + isMissingObject verdict.
	// This is the diagnostic surface for TPD-agreement issues — a frozen
	// stats feed (Frozen=true, a climbing SecsSinceOKPublish, a LastErr)
	// is exactly why TPD under-reports a visor's transports, and it's now
	// a query against the live visor (local or --via dmsg://<pk>).
	CXOFeeds []CXOFeedState `json:"cxo,omitempty"`

	// TPDLeafPub says whether the transport manager holds the CXO
	// transport-list snapshot publisher, and — when it does not — why.
	// Without it transport registration silently falls back to the HTTP
	// re-register path, which an empty .cxo alone does not distinguish
	// from "stats module still starting".
	TPDLeafPub *TPDLeafPublisherState `json:"tpd_leaf_publisher,omitempty"`

	// Proxy is the visor-side live proxystatus snapshot for the skysocks
	// surface — the same per-leg mux telemetry, running flag, and (when the
	// client pushes it) range-split summary the status.skysocks page renders.
	// It is OPT-IN: built only for `--select proxy`, never in the full/default
	// snapshot, so the default payload is unchanged.
	Proxy *proxystatus.Snapshot `json:"proxy,omitempty"`

	// Diag is the in-process plumbing view (router intake, transport read
	// queue and handlers, VStream muxes, dmsg ping/relay state, Go runtime) —
	// see DiagSnapshot. Cheap; part of the default snapshot.
	Diag *DiagSnapshot `json:"diag,omitempty"`

	// Roles is what this visor is FOR the network: the in-process dmsg
	// server (if any, and on which key/address), both directions of the dmsg
	// relay, and whether it refuses transit. See RolesSnapshot. Cheap; part
	// of the default snapshot.
	Roles *RolesSnapshot `json:"roles,omitempty"`

	// Notes collects per-section errors ("routing: <err>") so the snapshot is
	// self-describing about what it could and could not read.
	Notes []string `json:"notes,omitempty"`
}

// State*-select keys name the projectable subtrees of a StateSnapshot. Passing a
// subset to StateSnapshotProjected makes the SERVER build and marshal ONLY those
// sections, so a cheap `--select mux` skips the expensive transports build (the
// full snapshot is ~900 KB, dominated by transports at ~307 KB; mux is ~75 KB).
// The keys match the snapshot's JSON field names (or a short alias) so a --jq
// expression written against the full snapshot transfers to a projection
// unchanged.
const (
	SelectSummary    = "summary"    // summary
	SelectHealth     = "health"     // health + service_health
	SelectRouting    = "routing"    // routing_stats + route_groups + routing_policy + router_config
	SelectMux        = "mux"        // mux_route_groups + mux_counters (+ route_groups count)
	SelectPool       = "pool"       // pool: the standby-tunnel pool table
	SelectApps       = "apps"       // apps
	SelectTransports = "transports" // transports + persistent_transports
	SelectModules    = "modules"    // modules
	SelectCXO        = "cxo"        // cxo feed publish-health
	SelectProxy      = "proxy"      // visor-side proxystatus snapshot (skysocks); opt-in only
	SelectDiag       = "diag"       // router intake, transport queues/handlers, vstream, dmsg ping/relay, runtime
	SelectRoles      = "roles"      // in-process dmsg server, dmsg relay (both directions), transit refusal
)

// StateSelectKeys is the documented set of --select keys, in help order.
var StateSelectKeys = []string{
	SelectSummary, SelectHealth, SelectRouting, SelectMux, SelectPool,
	SelectApps, SelectTransports, SelectModules, SelectCXO, SelectProxy, SelectDiag,
	SelectRoles,
}

// stateSelectAliases maps a snapshot JSON field name onto the --select key
// that builds it, for the sections whose key is a short alias rather than the
// field name. The doc comment above promises "the keys match the snapshot's
// JSON field names (or a short alias)", and `--select mux_route_groups`
// silently built NOTHING (an unrecognized key matches no section) — which reads
// as "this visor has no mux route groups" rather than "wrong key".
var stateSelectAliases = map[string]string{
	"mux_route_groups":      SelectMux,
	"routing_stats":         SelectRouting,
	"route_groups":          SelectRouting,
	"routing_policy":        SelectRouting,
	"router_config":         SelectRouting,
	"service_health":        SelectHealth,
	"persistent_transports": SelectTransports,
}

// StateFieldSet is the parsed --select set. A nil set means "everything in the
// default full snapshot" (proxy stays opt-in even then). An entry present but
// unknown is ignored here and surfaced as a Note by the builder.
type StateFieldSet map[string]bool

// NewStateFieldSet parses the requested field keys. An empty/nil fields slice
// returns a nil set, i.e. build the full default snapshot.
func NewStateFieldSet(fields []string) StateFieldSet {
	if len(fields) == 0 {
		return nil
	}
	set := make(StateFieldSet, len(fields))
	for _, f := range fields {
		if f == "" {
			continue
		}
		if alias, ok := stateSelectAliases[f]; ok {
			f = alias
		}
		set[f] = true
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// Has reports whether section k should be built. A nil set (no --select) builds
// every default section; proxy is never a default section (see wantProxy).
func (s StateFieldSet) Has(k string) bool {
	if k == SelectProxy {
		return s != nil && s[k]
	}
	return s == nil || s[k]
}

// PoolTunnelInfo is one row of the standby-tunnel pool table (`visor state
// --select pool`). Every field is projected from the leading leg of a
// MuxRouteGroupInfo entry whose TunnelRole is "standby" — see poolTableFrom.
type PoolTunnelInfo struct {
	// LocalPort is the standby route group's local port (Desc.SrcPort on
	// this end) — the same port the pool arbiter and `mux_route_groups`
	// already key on.
	LocalPort routing.Port `json:"local_port"`
	// FirstHopPK/TpType are the leading leg's remote pk and transport type.
	FirstHopPK string `json:"first_hop_pk,omitempty"`
	TpType     string `json:"tp_type,omitempty"`
	// Hops is the leading leg's forward hop count (1 for a direct route).
	Hops int `json:"hops"`
	// CapacityPriorBps is the leading leg's PRIOR throughput estimate — the
	// value the pool ranks candidates by before it has measured one.
	CapacityPriorBps float64 `json:"capacity_prior_bps,omitempty"`
	// AuditionAgeMS is how long this standby tunnel has been held open,
	// pinged and measured without carrying a stream (MuxRouteGroupInfo.AgeMS).
	AuditionAgeMS float64 `json:"audition_age_ms,omitempty"`
	// LegSource names where the leading leg came from when it is not this
	// group's own dial (e.g. "standby :4, re-homed in place").
	LegSource string `json:"leg_source,omitempty"`
	// Legs is this standby tunnel's total leg count.
	Legs int `json:"legs"`
}

// EffectiveRoutingConfig is the routing configuration in force at
// runtime. MinHops/MuxRoutes/ForceLocalRoutes/ExistingTPOnly/
// TransportPreference are read from the live router (GetRouterSettings),
// so they reflect any runtime set that diverged from the config file;
// EnableCascadeRouteSetup and PolicyPerDial are the configured source
// (PolicyPerDial is a path/inline-source string, never a secret).
type EffectiveRoutingConfig struct {
	MinHops                 uint16   `json:"min_hops"`
	ForceLocalRoutes        bool     `json:"force_local_routes"`
	ExistingTPOnly          bool     `json:"existing_tp_only"`
	TransportPreference     []string `json:"transport_preference,omitempty"`
	EnableCascadeRouteSetup bool     `json:"enable_cascade_route_setup"`
	PolicyPerDial           string   `json:"policy_per_dial,omitempty"`
}

// ModulePresence reports which optional subsystems are wired on this visor.
// A false here means the module was not configured/started (nil), which is
// often exactly the thing being debugged ("why is there no stats feed?").
type ModulePresence struct {
	StatsTracker       bool `json:"stats_tracker"`
	UptimeRecorder     bool `json:"uptime_recorder"`
	EmbeddedTPS        bool `json:"embedded_transport_setup"`
	EmbeddedRouteSetup bool `json:"embedded_route_setup"`

	// ExecWasm describes the js/wasm command module this binary serves to
	// browser desks. Queryable rather than log-only on purpose: `go build .`
	// embeds whatever pkg/wasmhv/execwasm/blob already holds instead of
	// rebuilding it, so a current binary can serve a module many commits
	// old, and the only other signal is a startup warning that scrolls past.
	ExecWasm *ExecWasmInfo `json:"exec_wasm,omitempty"`
}

// ExecWasmInfo reports the embedded js/wasm command module's provenance.
type ExecWasmInfo struct {
	// Present is false for a plain source build, which embeds only the
	// placeholder README and falls back to an on-disk module.
	Present bool `json:"present"`
	// Revision is the commit the module was built from, recorded beside it
	// by `make embed-exec-wasm`. Empty when the module predates that.
	Revision string `json:"revision,omitempty"`
	// BinaryRevision is this visor's own commit, for comparison.
	BinaryRevision string `json:"binary_revision,omitempty"`
	// Stale is true when both revisions are known and differ: the desk is
	// serving older code than the visor. Fix with `make embed-exec-wasm`.
	Stale bool `json:"stale"`
	// Stamp is the served content fingerprint, which the page polls.
	Stamp string `json:"stamp,omitempty"`
}

// RolesSnapshot is the `roles` section of a StateSnapshot.
type RolesSnapshot struct {
	// PK is the visor's own public key — the key the own-key dmsg server and
	// the relay acceptor both answer under.
	PK cipher.PubKey `json:"pk"`
	// NoTransit reports routing.no_transit: this visor refuses to be an
	// INTERMEDIATE hop on someone else's route (its own edges still work).
	NoTransit bool `json:"no_transit"`

	DmsgServer DmsgServerRole `json:"dmsg_server"`
	DmsgRelay  DmsgRelayRole  `json:"dmsg_relay"`
}

// DmsgServerRole is the in-process dmsg server (dmsg.server.* in the config).
type DmsgServerRole struct {
	// Enabled is what the config asks for; Running is what actually started.
	// Enabled && !Running is the interesting case — e.g. no dmsg discovery PK
	// to register with, so the server no-opped at init.
	Enabled bool `json:"enabled"`
	Running bool `json:"running"`
	// Mode distinguishes the two servers a visor can host: "own_key" is the
	// bare server sharing the visor's PK/SK and dmsg client, "config_path" is
	// the whole standalone dmsg-server service run in-process from its own
	// config file, on its OWN key. Empty when no server is configured.
	Mode string `json:"mode,omitempty"`
	// PK is the key the server registers under: the visor's own key in
	// "own_key" mode, the config file's key in "config_path" mode.
	PK cipher.PubKey `json:"pk,omitempty"`
	// OwnKey is true when the server shares the visor's identity, i.e. one
	// discovery entry carries both the Client and the Server section.
	OwnKey bool `json:"own_key"`
	// SharedTransportPort: the server took the dmsg branch of the transport
	// port's TCP multiplexer instead of opening a listener of its own, so no
	// second port is bound or forwarded.
	SharedTransportPort bool   `json:"shared_transport_port"`
	LocalAddress        string `json:"local_address,omitempty"`
	PublicAddress       string `json:"public_address,omitempty"`
	ConfigPath          string `json:"config_path,omitempty"`
	// StartedAt is when the server was started (zero when it is not running).
	StartedAt time.Time `json:"started_at,omitempty"`
	// SessionCount is how many dmsg sessions this server currently holds, and
	// Clients names them. Answering "who is actually on this dmsg server" used
	// to require asking the discovery — which reports what CLIENTS claim about
	// their delegated servers, not what the server itself is holding, and says
	// nothing about how much traffic each one is running. A co-resident visor
	// knows the real answer; this reports it.
	//
	// Peers are separated from ordinary clients because a server-to-server link
	// is not a client at all: counting the two together makes a server look
	// busier than it is, and the peer mesh is the part an operator tunes
	// separately.
	//
	// Only populated in own_key mode — config_path mode runs the server inside
	// an unexported service type that exposes no session handle.
	SessionCount int                `json:"session_count"`
	PeerCount    int                `json:"peer_count"`
	Clients      []DmsgServerClient `json:"clients,omitempty"`
}

// DmsgServerClient is one session held by this visor's in-process dmsg server:
// the connected key, how many streams it has open, and whether the session is
// another dmsg server (a peer link) rather than a client.
type DmsgServerClient struct {
	PK      cipher.PubKey `json:"pk"`
	Streams int           `json:"streams"`
	Peer    bool          `json:"peer,omitempty"`
}

// DmsgRelayRole is both directions of the dmsg relay: the relays this visor
// attaches to, and the peers it relays for.
type DmsgRelayRole struct {
	// Port is the dmsg port the relay acceptor listens on over skynet.
	Port uint16 `json:"relay_port"`
	// RelayPeers are the peers this visor NOMINATED as its relays
	// (relayNominees → dmsg.Client.SetRelayPeers). Same field name and
	// meaning as `dmsg sessions`.
	RelayPeers []cipher.PubKey `json:"relay_peers,omitempty"`
	// Attached is the subset of RelayPeers this visor currently holds a
	// session with, and the carrier that session rides. Carrier "skynet" is
	// an actual relay attachment; anything else means the nominee was reached
	// as a plain dmsg server instead.
	Attached []DmsgRelayAttachment `json:"attached,omitempty"`
	// RelayClients are the peers attached TO this visor's relay acceptor,
	// each with the streams open on its relay session. Same field name and
	// meaning as `dmsg sessions`, which reports the keys alone.
	RelayClients []DmsgRelayClient `json:"relay_clients,omitempty"`
	// RelayedStreams is the relay slots in use across all attached peers, and
	// MaxRelayedStreams the cap they are charged against (dmsg.relay_max_streams).
	// A cap of 0 or less means this visor relays for nobody.
	RelayedStreams    int `json:"relayed_streams"`
	MaxRelayedStreams int `json:"max_relayed_streams"`
	// RelayRefused counts stream requests turned away at capacity (dmsg error
	// 308) since start. Nonzero means this hub refused traffic it was asked to
	// carry — at the dialer that refusal is indistinguishable from the
	// destination being down, so without this it is only ever diagnosed from
	// the wrong end.
	RelayRefused int `json:"relay_refused"`
}

// DmsgRelayAttachment is one nominated relay this visor holds a session with.
type DmsgRelayAttachment struct {
	PK        cipher.PubKey `json:"pk"`
	Carrier   string        `json:"carrier"`
	Streams   int           `json:"streams"`
	LatencyMS float64       `json:"latency_ms"`
}

// DmsgRelayClient is a peer attached to this visor as its dmsg relay.
type DmsgRelayClient struct {
	PK      cipher.PubKey `json:"pk"`
	Streams int           `json:"streams"`
}

// DiagSnapshot is the `diag` section of a StateSnapshot.
type DiagSnapshot struct {
	Runtime DiagRuntime `json:"runtime"`
	// Panics is the recovered-panic accounting: how many have happened since
	// start and the most recent few with their stacks. An UNrecovered panic
	// goes to local/log/skywire-crash.log (debug.SetCrashOutput, see
	// storeLog); this is the other half, which nothing else records. Omitted
	// when none have happened, so its presence is the signal.
	Panics *DiagPanics `json:"panics,omitempty"`
	// TransportReadQueue is the shared queue every transport read loop feeds
	// and the router drains; at capacity, every transport stalls behind the
	// router (pings and pongs included).
	TransportReadQueue *DiagQueue `json:"transport_read_queue,omitempty"`
	// Intake is the router's inbound-path view: unknown/control packets by
	// type, stale-route drops, and each route group's app queue depth.
	Intake *router.IntakeStats `json:"intake,omitempty"`
	// RouteSource is where multi-hop routes came from: the local graph (a
	// hypervisor's attached visors), the route finder, or the local fallback.
	RouteSource *router.RouteSourceStats `json:"route_source,omitempty"`
	// RouteSetup is how this visor's route setups were ISSUED: batched vs
	// single vs fell back because the setup node is un-upgraded, plus whether
	// the RSN-oracle queried once per fill and whether the concurrent dials of a
	// fill landed on distinct intermediates.
	RouteSetup *router.SetupPathStats `json:"route_setup,omitempty"`
	// VStream is one entry per virtual-stream mux (skynet forwarding,
	// app-direct dials, visor RPC): open streams, relay legs, and frames that
	// arrived for streams this side does not have.
	VStream []DiagVStreamMux `json:"vstream,omitempty"`
	// Dmsg is per-session ping health plus the relay nominee/backoff state.
	Dmsg *DiagDmsg `json:"dmsg,omitempty"`
	// Transports is per-transport liveness: missed pongs, last packet age,
	// and which route-ID-0 handlers are wired (a missing one means that
	// packet type is dropped by the router — #4725).
	Transports []DiagTransport `json:"transports,omitempty"`
	// TransportEvents is the last transport.TransportEventRingSize opens and
	// closes, oldest first, each close with the reason the code gave. This is
	// where to look when a transport that was in `tp ls` is gone: the visor log
	// ring holds minutes; this holds the events.
	TransportEvents []transport.TransportEvent `json:"transport_events,omitempty"`
	// TransportLastClose is the last close event of each of the last
	// transport.TransportLastCloseMax transports that closed, keyed by
	// transport id. TransportEvents is a ring and on a public visor it is
	// flooded by autoconnect opens — 256 entries spanned three minutes on one
	// exit visor — so the one close being chased was already gone. This
	// survives any amount of open churn: look up the transport id here to get
	// the reason it died and how old it was.
	TransportLastClose map[uuid.UUID]transport.TransportEvent `json:"transport_last_close,omitempty"`
	// MuxEvents is the last router.MuxEventRingSize route-group and mux-leg
	// changes, oldest first, each with the reason the code gave and who
	// initiated it (local/remote/operator/adaptive/policy). This is where to
	// look when a leg that was in `proxy mux info` is gone and
	// transport_events shows its transport still open — the removal came from
	// the router, not the transport layer.
	MuxEvents []router.MuxEvent `json:"mux_events,omitempty"`
}

// DiagRuntime is the Go runtime at a glance.
type DiagRuntime struct {
	Goroutines  int     `json:"goroutines"`
	HeapAllocMB float64 `json:"heap_alloc_mb"`
	SysMB       float64 `json:"sys_mb"`
	NumGC       uint32  `json:"num_gc"`
	GoVersion   string  `json:"go_version"`
}

// DiagPanics is the recovered-panic accounting. Count keeps rising after Last
// has wrapped, so the two together say whether what Last shows is the whole
// story.
type DiagPanics struct {
	Count uint64               `json:"count"`
	Last  []logging.PanicEntry `json:"last,omitempty"`
}

// DiagQueue is a bounded queue's depth and capacity.
type DiagQueue struct {
	Depth    int `json:"depth"`
	Capacity int `json:"capacity"`
}

// DiagVStreamMux names one VStream mux and carries its counters, plus one row
// per live stream. The rows are what turn "streams: 27" into an answer: which
// app (or none) opened each one, over which transport, how long ago, and how
// deep its inbound queue is right now.
type DiagVStreamMux struct {
	Name string `json:"name"`
	transport.VStreamMuxStats
	StreamList []transport.VStreamInfo `json:"stream_list,omitempty"`
}

// DiagDmsg is the dmsg client's session and relay state.
type DiagDmsg struct {
	// Unpublished: this client publishes no discovery entry (a browser visor).
	Unpublished   bool              `json:"unpublished"`
	Sessions      []DiagDmsgSession `json:"sessions,omitempty"`
	RelayNominees []cipher.PubKey   `json:"relay_nominees,omitempty"`
	RelayingFor   []DiagRelayClient `json:"relaying_for,omitempty"`
	RelayBackoff  []DiagRelayTimer  `json:"relay_backoff,omitempty"`
	RelayDialSkip []DiagRelayTimer  `json:"relay_dial_skip,omitempty"`
}

// DiagDmsgSession is one dmsg session's liveness.
type DiagDmsgSession struct {
	PK        cipher.PubKey `json:"pk"`
	Carrier   string        `json:"carrier"`
	Protocol  string        `json:"protocol"`
	Streams   int           `json:"streams"`
	LatencyMS float64       `json:"latency_ms"`
	// PingFails is the consecutive liveness-ping failures; 2 closes the session.
	PingFails int `json:"ping_fails"`
}

// DiagRelayClient is a peer attached to this visor as its dmsg relay.
type DiagRelayClient struct {
	PK      cipher.PubKey `json:"pk"`
	Streams int           `json:"streams"`
}

// DiagRelayTimer is a relay-side backoff or skip with its remaining time.
type DiagRelayTimer struct {
	PK         cipher.PubKey `json:"pk"`
	RemainingS float64       `json:"remaining_s"`
}

// DiagTransport is one transport's liveness and wiring.
type DiagTransport struct {
	ID          uuid.UUID     `json:"id"`
	Remote      cipher.PubKey `json:"remote"`
	Type        string        `json:"type"`
	MissedPongs int64         `json:"missed_pongs"`
	PongSeen    bool          `json:"pong_seen"`
	// LastRecvAgoS is seconds since the read loop last received any packet
	// (-1 = never). A transport with pongs flowing but climbing app-level
	// timeouts is the router-intake case, not a link problem.
	LastRecvAgoS float64 `json:"last_recv_ago_s"`
	// MalformedFrames counts frames whose type byte was outside the known
	// range — the peer wrote a packet whose size field did not match it.
	MalformedFrames int64    `json:"malformed_frames"`
	Handlers        []string `json:"handlers,omitempty"`
}
