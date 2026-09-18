// Package router pkg/router/settings_group.go c2-net-routing
//
// Per-route-group resolution of the knob catalog.
//
// `route settings --app <name> k=v` sets an override map for the route groups
// a given app owns, so a subject client and its paired reference client can run
// different values on one visor. The app name is the one the visor already
// knows: the tag saveRouteGroupRules attaches from the appserver proc's dial
// options (RouteGroup.SetAppName).
//
// Resolution happens ONCE when the group is built, again when the app tag
// arrives, and thereafter only when the catalog version moves — the group's
// send-window service loop calls refreshKnobs on its tick, which is the change
// notification. A read on the data path is one atomic pointer load; nothing
// resolves per packet.
package router

import (
	"time"

	"github.com/skycoin/skywire/pkg/router/routersettings"
)

// knobs returns the resolved knob view for this route group's owning app.
// Nil-safe, and a nil view reads the visor-wide values, so a group built before
// its holder was attached still behaves.
func (rg *RouteGroup) knobs() *routersettings.View {
	if rg == nil {
		return nil
	}
	return rg.knobHolder.View()
}

// refreshKnobs re-resolves the group's view if the catalog moved. Reports
// whether it did, so a service loop can reset its ticker on the same tick.
func (rg *RouteGroup) refreshKnobs() bool {
	if rg == nil {
		return false
	}
	return rg.knobHolder.Refresh()
}

// setKnobApp re-points the group (and its mux) at an app's overrides.
func (rg *RouteGroup) setKnobApp(app string) {
	if rg == nil || rg.knobHolder == nil {
		return
	}
	rg.knobHolder.SetApp(app)
}

// knobs returns the resolved knob view for the route group that owns this mux.
func (m *routeMux) knobs() *routersettings.View {
	if m == nil {
		return nil
	}
	return m.knobHolder.View()
}

// The per-group read helpers. Each is one atomic pointer load plus a slice
// index, which is why a use site on the send path can read a knob directly
// instead of caching it.

// knRatio reads a KindRatio knob through this group's view.
func (rg *RouteGroup) knRatio(k *routersettings.Knob) float64 { return rg.knobs().Ratio(k) }

// knDur reads a KindDuration knob through this group's view.
func (rg *RouteGroup) knDur(k *routersettings.Knob) time.Duration { return rg.knobs().Duration(k) }

// knInt reads a KindCount knob through this group's view.
func (rg *RouteGroup) knInt(k *routersettings.Knob) int { return rg.knobs().Int(k) }

// knBool reads a KindBool knob through this group's view.
func (rg *RouteGroup) knBool(k *routersettings.Knob) bool { return rg.knobs().Bool(k) }

// knRatio reads a KindRatio knob through the owning group's view.
func (m *routeMux) knRatio(k *routersettings.Knob) float64 { return m.knobs().Ratio(k) }

// knDur reads a KindDuration knob through the owning group's view.
func (m *routeMux) knDur(k *routersettings.Knob) time.Duration { return m.knobs().Duration(k) }

// knInt reads a KindCount knob through the owning group's view.
func (m *routeMux) knInt(k *routersettings.Knob) int { return m.knobs().Int(k) }

// knBytes reads a KindBytes knob through the owning group's view.
func (m *routeMux) knBytes(k *routersettings.Knob) int64 { return m.knobs().Bytes(k) }
