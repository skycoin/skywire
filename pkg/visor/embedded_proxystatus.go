// Package visor pkg/visor/embedded_proxystatus.go c3-vis-core
//
// visorStatusProvider implements pkg/proxystatus.Provider on top of the visor's
// existing read APIs, so the resolving proxies (dmsg_web / skynet_web) can serve
// each surface's reserved in-process status host (http://status.<surface>/).
//
// It is deliberately READ-ONLY and reuses APIs that already exist:
//
//   - logs:   Visor.LogsSince(app)         — the app's bbolt log store tail
//   - mux:    Visor.RouteGroupMuxInfo(app)  — the same per-leg telemetry the
//     `cli proxy mux plot` renderer consumes
//   - running: procManager.ProcByName(app)
//
// Route CONTROL is intentionally out of scope for the MVP; the extension seam
// lives in pkg/proxystatus (Snapshot doc + the disabled control section). When
// it lands, a mutating method is added to proxystatus.Provider and implemented
// here on top of the visor's existing mux-reshape API (AddMuxRoute /
// RemoveMuxRoute / SetMuxMode), which already exists — no new plumbing.
package visor

import (
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// statusLogLookback bounds how far back the status page's log tail reaches. The
// page renders the most recent lines within this window (capped again in the
// renderer), so it shows current activity without scanning the whole store.
const statusLogLookback = 30 * time.Minute

// statusEventsCap bounds how many recent route/transport event lines the status
// page carries for a surface. The ring itself is bounded (pkg/logging), but the
// snapshot takes only the most recent tail so the page stays readable.
const statusEventsCap = 200

// statusMaxLogLines mirrors the renderer's maxLogLines cap so the snapshot never
// carries more log lines than the page would render.
const statusMaxLogLines = 200

// tailRecordLines formats the most recent up-to-cap Records into log lines
// (oldest first), matching the visor's live log format.
func tailRecordLines(recs []logging.Record, limit int) []string {
	if len(recs) > limit {
		recs = recs[len(recs)-limit:]
	}
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.Format())
	}
	return out
}

// surfaceApp maps a status surface to the underlying app / logger name whose
// logs and route group the page reflects. skysocks is the highest-value view:
// its route group is where multiplexing actually happens.
func surfaceApp(s proxystatus.Surface) string {
	switch s {
	case proxystatus.SurfaceDmsg:
		return skyenvDmsgWebApp
	case proxystatus.SurfaceSkynet:
		return skyenvSkynetWebApp
	case proxystatus.SurfaceSkysocks:
		return skyenv.SkysocksClientName
	default:
		return ""
	}
}

// visorStatusProvider adapts *Visor to proxystatus.Provider.
type visorStatusProvider struct{ v *Visor }

// proxyStatusProvider returns the provider injected into the resolving proxies'
// runtime configs. Returns nil-typed provider only when the visor is nil.
func (v *Visor) proxyStatusProvider() proxystatus.Provider { return &visorStatusProvider{v: v} }

// StatusSnapshot builds the read-only snapshot for a surface. A missing piece
// (no log store, no active route group) degrades to an empty section with a
// note rather than failing the whole page — a status page must render even when
// the surface is idle or half-up.
func (p *visorStatusProvider) StatusSnapshot(surface proxystatus.Surface) (proxystatus.Snapshot, error) {
	app := surfaceApp(surface)
	if app == "" {
		return proxystatus.Snapshot{Surface: surface}, fmt.Errorf("unknown surface %q", surface)
	}
	snap := proxystatus.Snapshot{Surface: surface, App: app}

	if p.v.procM != nil {
		proc, ok := p.v.procM.ProcByName(app)
		snap.Running = ok && proc != nil
	}

	// Events + Logs come from the log broadcaster's bounded per-app ring — the
	// same Fire tap that feeds `proxy start --verbose`. Events are the layered
	// route/transport lifecycle lines (route requests, setup-node connects,
	// route creation, RSN-oracle legs, handshakes, mux-enable/SACK, mux route
	// established, distribution, self-heal) tagged app_name=<app>; ring "logs"
	// are the app's own output.
	ringEvents, ringLogs := p.v.RecentAppEvents(app, logrus.DebugLevel)
	if len(ringEvents) > 0 {
		snap.Events = tailRecordLines(ringEvents, statusEventsCap)
	}

	// Logs: prefer the richer scoped log captured in the ring (app stdout +
	// tagged lifecycle). Fall back to the app's bbolt log store when the ring
	// has nothing yet, so an idle-but-previously-run surface still shows a tail.
	if len(ringLogs) > 0 {
		snap.Logs = tailRecordLines(ringLogs, statusMaxLogLines)
	} else if logs, err := p.v.LogsSince(time.Now().Add(-statusLogLookback), app); err == nil {
		snap.Logs = logs
	} else {
		snap.Note = appendNote(snap.Note, "logs unavailable: "+err.Error())
	}

	// Mux legs (best-effort): the surface may have no active route group.
	if infos, err := p.v.RouteGroupMuxInfo(app); err == nil {
		if len(infos) > 0 {
			snap.MuxEnabled = infos[0].MuxEnabled
			for _, leg := range infos[0].Legs {
				snap.Legs = append(snap.Legs, proxystatus.Leg{
					Index:          leg.Index,
					TransportID:    leg.TransportID,
					TpType:         leg.TpType,
					RemotePK:       leg.RemotePK,
					LatencyMS:      leg.LatencyMS,
					RouteLatencyMS: leg.RouteLatencyMS,
					Direct:         leg.Direct,
					SentBytes:      leg.SentBytes,
					RecvBytes:      leg.RecvBytes,
					Retransmits:    leg.Retransmits,
					Alive:          leg.Alive,
					Standby:        leg.Standby,
					Hops:           proxyHopsFrom(leg.Hops),
				})
			}
		}
	} else {
		snap.Note = appendNote(snap.Note, "mux info unavailable: "+err.Error())
	}

	// Events: scaffold. Route/transport lifecycle events for a surface are not
	// yet collected into a per-surface buffer; the renderer shows an empty
	// section. This is the extension point that pairs with route control.
	return snap, nil
}

func appendNote(existing, add string) string {
	if existing == "" {
		return add
	}
	return existing + " · " + add
}

// proxyHopsFrom transcribes the visor's per-leg MuxHopInfo into the
// proxystatus.Hop shape the status page renders (full PKs preserved).
func proxyHopsFrom(hops []MuxHopInfo) []proxystatus.Hop {
	if len(hops) == 0 {
		return nil
	}
	out := make([]proxystatus.Hop, len(hops))
	for i, h := range hops {
		out[i] = proxystatus.Hop{
			TpID:      h.TpID,
			From:      h.From,
			To:        h.To,
			TpType:    h.TpType,
			LatencyMS: h.LatencyMS,
		}
	}
	return out
}
