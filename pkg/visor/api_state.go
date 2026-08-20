// Package visor pkg/visor/api_state.go c1-vis-core
package visor

import (
	"time"

	"github.com/skycoin/skywire/pkg/app/appserver"
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
	Modules       ModulePresence                   `json:"modules"`

	// Notes collects per-section errors ("routing: <err>") so the snapshot is
	// self-describing about what it could and could not read.
	Notes []string `json:"notes,omitempty"`
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

// StateSnapshot assembles the runtime StateSnapshot. Each section is best-effort
// and never panics on a not-yet-initialized subsystem; failures are recorded in
// Notes. Secrets are never included (see the StateSnapshot type doc).
func (v *Visor) StateSnapshot() (*StateSnapshot, error) {
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

	if sum, err := v.Summary(); err != nil {
		note("summary", err)
	} else {
		snap.Summary = sum
	}

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

	if rs, err := v.RoutingStats(); err != nil {
		note("routing_stats", err)
	} else {
		snap.RoutingStats = &rs
	}

	if rgs, err := v.RouteGroups(); err != nil {
		note("route_groups", err)
	} else {
		snap.RouteGroups = len(rgs)
	}

	if rp, err := v.RoutingPolicies(); err != nil {
		note("routing_policy", err)
	} else {
		snap.RoutingPolicy = rp
	}

	if apps, err := v.Apps(); err != nil {
		note("apps", err)
	} else {
		snap.Apps = apps
	}

	if tps, err := v.Transports(nil, nil, false); err != nil {
		note("transports", err)
	} else {
		snap.Transports = tps
	}

	if pts, err := v.GetPersistentTransports(); err != nil {
		note("persistent_transports", err)
	} else {
		snap.Persistent = pts
	}

	snap.Modules = ModulePresence{
		StatsTracker:       v.statsTracker != nil,
		UptimeRecorder:     v.uptimeRecorder != nil,
		EmbeddedTPS:        v.embeddedTPS != nil,
		EmbeddedRouteSetup: v.embeddedRouteSetup != nil,
	}

	return snap, nil
}
