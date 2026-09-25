// Package visor pkg/visor/api_state.go c3-vis-core
package visor

import (
	"time"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/wasmhv/execwasm"
)

// poolTableFrom projects the standby-tunnel pool table out of mrgs — the
// SAME MuxRouteGroupInfo slice the mux section builds — so the pool table
// costs nothing beyond the AllRouteGroupMuxInfo call already made for it.
func poolTableFrom(mrgs []visorapi.MuxRouteGroupInfo) []visorapi.PoolTunnelInfo {
	var out []visorapi.PoolTunnelInfo
	for _, mrg := range mrgs {
		if mrg.TunnelRole != "standby" {
			continue
		}
		row := visorapi.PoolTunnelInfo{
			LocalPort:     mrg.Desc.SrcPort,
			AuditionAgeMS: mrg.AgeMS,
			Legs:          len(mrg.Legs),
		}
		if len(mrg.Legs) > 0 {
			leg := mrg.Legs[0]
			row.FirstHopPK = leg.RemotePK
			row.TpType = leg.TpType
			row.Hops = len(leg.Hops)
			row.CapacityPriorBps = leg.CapacityPriorBps
			row.LegSource = leg.Source
		}
		out = append(out, row)
	}
	return out
}

// StateSnapshot assembles the full runtime StateSnapshot (every default
// section). It is StateSnapshotProjected(nil).
func (v *Visor) StateSnapshot() (*visorapi.StateSnapshot, error) {
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
func (v *Visor) StateSnapshotProjected(fields []string) (*visorapi.StateSnapshot, error) {
	want := visorapi.NewStateFieldSet(fields)
	snap := &visorapi.StateSnapshot{At: time.Now()}
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

	if want.Has(visorapi.SelectSummary) {
		if sum, err := v.Summary(); err != nil {
			note("summary", err)
		} else {
			snap.Summary = sum
		}
	}

	if want.Has(visorapi.SelectHealth) {
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

	if want.Has(visorapi.SelectRouting) {
		if rs, err := v.RoutingStats(); err != nil {
			note("routing_stats", err)
		} else {
			snap.RoutingStats = &rs
		}
		if rc, err := v.GetRouterSettings(); err != nil {
			note("router_config", err)
		} else {
			erc := &visorapi.EffectiveRoutingConfig{
				MinHops:             rc.MinHops,
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
	if want.Has(visorapi.SelectRouting) || want.Has(visorapi.SelectMux) {
		if rgs, err := v.RouteGroups(); err != nil {
			note("route_groups", err)
		} else {
			snap.RouteGroups = len(rgs)
		}
	}

	// mux and pool share ONE AllRouteGroupMuxInfo call: pool is a filtered
	// projection of the exact same entries, never computed twice.
	if want.Has(visorapi.SelectMux) || want.Has(visorapi.SelectPool) {
		if mrgs, err := v.AllRouteGroupMuxInfo(); err != nil {
			note("mux_route_groups", err)
		} else {
			if want.Has(visorapi.SelectMux) && len(mrgs) > 0 {
				snap.MuxRouteGroups = mrgs
			}
			if want.Has(visorapi.SelectPool) {
				if pool := poolTableFrom(mrgs); len(pool) > 0 {
					snap.Pool = pool
				}
			}
		}
	}

	if want.Has(visorapi.SelectMux) && v.router != nil {
		mc := v.router.MuxCounters()
		snap.MuxCounters = &mc
	}

	if want.Has(visorapi.SelectApps) {
		if apps, err := v.Apps(); err != nil {
			note("apps", err)
		} else {
			snap.Apps = apps
		}
	}

	if want.Has(visorapi.SelectTransports) {
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

	if want.Has(visorapi.SelectModules) {
		snap.Modules = &visorapi.ModulePresence{
			StatsTracker:       v.statsTracker != nil,
			UptimeRecorder:     v.uptimeRecorder != nil,
			EmbeddedTPS:        v.embeddedTPS != nil,
			EmbeddedRouteSetup: v.embeddedRouteSetup != nil,
			ExecWasm:           execWasmInfo(),
		}
	}

	if want.Has(visorapi.SelectCXO) {
		if cf := v.CXOFeedStates(); len(cf) > 0 {
			snap.CXOFeeds = cf
		}
		snap.TPDLeafPub = v.tpdLeafPublisherState()
	}

	if want.Has(visorapi.SelectDiag) {
		snap.Diag = v.DiagSnapshot()
	}

	if want.Has(visorapi.SelectRoles) {
		snap.Roles = v.RolesSnapshot()
	}

	// proxy is opt-in (never in the default snapshot): the visor-side
	// proxystatus snapshot for the skysocks surface — per-leg mux telemetry,
	// running flag, and the range-split summary when the client has pushed it.
	if want.Has(visorapi.SelectProxy) {
		if ps, err := v.proxyStatusProvider().StatusSnapshot(proxystatus.SurfaceSkysocks); err != nil {
			note("proxy", err)
		} else {
			snap.Proxy = &ps
		}
	}

	return snap, nil
}

// execWasmInfo describes the embedded js/wasm command module for the state
// snapshot. Nil when nothing is embedded and nothing is recorded — a plain
// source build has no module and no story to tell about one.
func execWasmInfo() *visorapi.ExecWasmInfo {
	present := execwasm.Present()
	rev := execwasm.Revision()
	if !present && rev == "" {
		return nil
	}
	bin := buildinfo.Commit()
	if bin == "unknown" {
		bin = ""
	}
	return &visorapi.ExecWasmInfo{
		Present:        present,
		Revision:       rev,
		BinaryRevision: bin,
		Stale:          rev != "" && bin != "" && rev != bin,
		Stamp:          execwasm.Stamp(),
	}
}
