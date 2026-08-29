// Package visor pkg/visor/api_state.go c3-vis-core
package visor

import (
	"time"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/proxystatus"
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

	// CXOFeeds is the live publish-health of each CXO feed (system
	// telemetry/tp-list feed first, then user feeds): dirty state, secs
	// since last OK publish, in-memory leaf/node counts, and any standing
	// publish error with its concrete type + isMissingObject verdict.
	// This is the diagnostic surface for TPD-agreement issues — a frozen
	// stats feed (Frozen=true, a climbing SecsSinceOKPublish, a LastErr)
	// is exactly why TPD under-reports a visor's transports, and it's now
	// a query against the live visor (local or --via dmsg://<pk>).
	CXOFeeds []CXOFeedState `json:"cxo,omitempty"`

	// Proxy is the visor-side live proxystatus snapshot for the skysocks
	// surface — the same per-leg mux telemetry, running flag, and (when the
	// client pushes it) range-split summary the status.skysocks page renders.
	// It is OPT-IN: built only for `--select proxy`, never in the full/default
	// snapshot, so the default payload is unchanged.
	Proxy *proxystatus.Snapshot `json:"proxy,omitempty"`

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
	SelectMux        = "mux"        // mux_route_groups (+ route_groups count)
	SelectApps       = "apps"       // apps
	SelectTransports = "transports" // transports + persistent_transports
	SelectModules    = "modules"    // modules
	SelectCXO        = "cxo"        // cxo feed publish-health
	SelectProxy      = "proxy"      // visor-side proxystatus snapshot (skysocks); opt-in only
)

// StateSelectKeys is the documented set of --select keys, in help order.
var StateSelectKeys = []string{
	SelectSummary, SelectHealth, SelectRouting, SelectMux,
	SelectApps, SelectTransports, SelectModules, SelectCXO, SelectProxy,
}

// stateFieldSet is the parsed --select set. A nil set means "everything in the
// default full snapshot" (proxy stays opt-in even then). An entry present but
// unknown is ignored here and surfaced as a Note by the builder.
type stateFieldSet map[string]bool

// newStateFieldSet parses the requested field keys. An empty/nil fields slice
// returns a nil set, i.e. build the full default snapshot.
func newStateFieldSet(fields []string) stateFieldSet {
	if len(fields) == 0 {
		return nil
	}
	set := make(stateFieldSet, len(fields))
	for _, f := range fields {
		if f != "" {
			set[f] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// has reports whether section k should be built. A nil set (no --select) builds
// every default section; proxy is never a default section (see wantProxy).
func (s stateFieldSet) has(k string) bool {
	if k == SelectProxy {
		return s != nil && s[k]
	}
	return s == nil || s[k]
}

// EffectiveRoutingConfig is the routing configuration in force at
// runtime. MinHops/MuxRoutes/ForceLocalRoutes/ExistingTPOnly/
// TransportPreference are read from the live router (GetRouterSettings),
// so they reflect any runtime set that diverged from the config file;
// EnableCascadeRouteSetup and PolicyPerDial are the configured source
// (PolicyPerDial is a path/inline-source string, never a secret).
type EffectiveRoutingConfig struct {
	MinHops                 uint16   `json:"min_hops"`
	MuxRoutes               int      `json:"mux_routes"`
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
}

// StateSnapshot assembles the full runtime StateSnapshot (every default
// section). It is StateSnapshotProjected(nil).
func (v *Visor) StateSnapshot() (*StateSnapshot, error) {
	return v.StateSnapshotProjected(nil)
}

// StateSnapshotProjected assembles the runtime StateSnapshot, building ONLY the
// sections named in fields (see the StateSelect* keys). A nil/empty fields slice
// builds the full default snapshot (proxy stays opt-in). This is the efficiency
// win behind `cli visor state --select`: `--select mux` skips the ~307 KB
// transports build entirely, so a 1 s mux watch is cheap.
//
// Each requested section is best-effort and never panics on a not-yet-
// initialized subsystem; failures are recorded in Notes. Secrets are never
// included (see the StateSnapshot type doc). At + Suspended are always populated
// (both cheap).
func (v *Visor) StateSnapshotProjected(fields []string) (*StateSnapshot, error) {
	want := newStateFieldSet(fields)
	snap := &StateSnapshot{At: time.Now()}
	note := func(section string, err error) {
		if err != nil {
			snap.Notes = append(snap.Notes, section+": "+err.Error())
		}
	}

	if susp, err := v.IsSuspended(); err != nil {
		note("suspended", err)
	} else {
		snap.Suspended = susp
	}

	if want.has(SelectSummary) {
		if sum, err := v.Summary(); err != nil {
			note("summary", err)
		} else {
			snap.Summary = sum
		}
	}

	if want.has(SelectHealth) {
		if h, err := v.Health(); err != nil {
			note("health", err)
		} else {
			snap.Health = h
		}
		if sh, err := v.ServiceHealth(); err != nil {
			note("service_health", err)
		} else {
			snap.ServiceHealth = sh
		}
	}

	if want.has(SelectRouting) {
		if rs, err := v.RoutingStats(); err != nil {
			note("routing_stats", err)
		} else {
			snap.RoutingStats = &rs
		}
		if rc, err := v.GetRouterSettings(); err != nil {
			note("router_config", err)
		} else {
			erc := &EffectiveRoutingConfig{
				MinHops:             rc.MinHops,
				MuxRoutes:           rc.MuxRoutes,
				ForceLocalRoutes:    rc.ForceLocalRoutes,
				ExistingTPOnly:      rc.ExistingTPOnly,
				TransportPreference: rc.TransportPreference,
			}
			if v.conf != nil && v.conf.Routing != nil {
				erc.EnableCascadeRouteSetup = v.conf.Routing.EnableCascadeRouteSetup
				erc.PolicyPerDial = v.conf.Routing.PolicyPerDial
			}
			snap.RouterConfig = erc
		}
		if rp, err := v.RoutingPolicies(); err != nil {
			note("routing_policy", err)
		} else {
			snap.RoutingPolicy = rp
		}
	}

	// route_groups is a cheap count wanted by both the routing and mux views.
	if want.has(SelectRouting) || want.has(SelectMux) {
		if rgs, err := v.RouteGroups(); err != nil {
			note("route_groups", err)
		} else {
			snap.RouteGroups = len(rgs)
		}
	}

	if want.has(SelectMux) {
		if mrgs, err := v.AllRouteGroupMuxInfo(); err != nil {
			note("mux_route_groups", err)
		} else if len(mrgs) > 0 {
			snap.MuxRouteGroups = mrgs
		}
	}

	if want.has(SelectApps) {
		if apps, err := v.Apps(); err != nil {
			note("apps", err)
		} else {
			snap.Apps = apps
		}
	}

	if want.has(SelectTransports) {
		// logs=true so each TransportSummary.Log carries the transport's
		// cumulative recv/sent byte counters — the passive throughput totals an
		// operator debugging a slow/idle link wants, surfaced without a second call.
		if tps, err := v.Transports(nil, nil, true); err != nil {
			note("transports", err)
		} else {
			snap.Transports = tps
		}
		if pts, err := v.GetPersistentTransports(); err != nil {
			note("persistent_transports", err)
		} else {
			snap.Persistent = pts
		}
	}

	if want.has(SelectModules) {
		snap.Modules = &ModulePresence{
			StatsTracker:       v.statsTracker != nil,
			UptimeRecorder:     v.uptimeRecorder != nil,
			EmbeddedTPS:        v.embeddedTPS != nil,
			EmbeddedRouteSetup: v.embeddedRouteSetup != nil,
		}
	}

	if want.has(SelectCXO) {
		if cf := v.CXOFeedStates(); len(cf) > 0 {
			snap.CXOFeeds = cf
		}
	}

	// proxy is opt-in (never in the default snapshot): the visor-side
	// proxystatus snapshot for the skysocks surface — per-leg mux telemetry,
	// running flag, and the range-split summary when the client has pushed it.
	if want.has(SelectProxy) {
		if ps, err := v.proxyStatusProvider().StatusSnapshot(proxystatus.SurfaceSkysocks); err != nil {
			note("proxy", err)
		} else {
			snap.Proxy = &ps
		}
	}

	return snap, nil
}
