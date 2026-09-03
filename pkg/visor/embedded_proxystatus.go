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
	"net"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsgweb"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skynetweb"
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
	// The procM guard matters: LogsSince dereferences the proc manager, and a
	// status page must still render on a half-initialized visor (the exact case
	// an operator hits one) rather than panicking on the request path.
	if len(ringLogs) > 0 {
		snap.Logs = tailRecordLines(ringLogs, statusMaxLogLines)
	} else if p.v.procM != nil {
		if logs, err := p.v.LogsSince(time.Now().Add(-statusLogLookback), app); err == nil {
			snap.Logs = logs
		} else {
			snap.Note = appendNote(snap.Note, "logs unavailable: "+err.Error())
		}
	}

	// Layer sections: for the two RESOLVING proxies the page's substance is the
	// layer itself (liveness, chain, sessions, names, forwards), not a route
	// group — they own no mux legs, and the renderer degrades to a layer-specific
	// note rather than an empty leg table. Filled from what each layer already
	// tracks; nothing is fabricated for a layer that does not know it.
	switch surface {
	case proxystatus.SurfaceDmsg:
		p.fillLayerBounded(&snap, "dmsg layer", p.fillDmsgLayer)
	case proxystatus.SurfaceSkynet:
		p.fillLayerBounded(&snap, "skynet layer", p.fillSkynetLayer)
	case proxystatus.SurfaceSkysocks:
		// The skysocks tunnel's state is Legs/Streams/RangeSplit — no layer section.
	}

	// Mux legs (best-effort): the surface may have no active route group. Each
	// route group is one --tunnels STREAM; its Legs are the PACKET-level mux. Model
	// every tunnel (not just the first) so the status tree can nest tunnel → legs;
	// mirror the first tunnel's legs into snap.Legs for back-compat.
	var self cipher.PubKey
	if p.v.conf != nil && p.v.conf.Common != nil {
		self = p.v.conf.PK
		snap.SelfPK = self.String()
	}
	if infos, err := p.v.RouteGroupMuxInfo(app); err == nil {
		for ti, info := range infos {
			// The exit is the descriptor end that is NOT this visor. A client route
			// group's descriptor carries the local visor as Dst and the exit as Src,
			// so hardcoding DstPK mislabeled the local visor as the exit; pick the
			// far end orientation-independently.
			exit := info.Desc.DstPK
			if exit == self {
				exit = info.Desc.SrcPK
			}
			t := proxystatus.Tunnel{
				Index:      ti,
				ExitPK:     exit.String(),
				MuxEnabled: info.MuxEnabled,
			}
			for _, leg := range info.Legs {
				t.Legs = append(t.Legs, proxyLegFrom(leg))
			}
			snap.Tunnels = append(snap.Tunnels, t)
		}
		if len(snap.Tunnels) > 0 {
			snap.MuxEnabled = snap.Tunnels[0].MuxEnabled
			snap.Legs = snap.Tunnels[0].Legs
		}
	} else {
		snap.Note = appendNote(snap.Note, "mux info unavailable: "+err.Error())
	}

	// Events: scaffold. Route/transport lifecycle events for a surface are not
	// yet collected into a per-surface buffer; the renderer shows an empty
	// section. This is the extension point that pairs with route control.
	return snap, nil
}

// resolverListenAddr renders a resolving proxy's SOCKS5 listener the way its
// runtime binds it: an empty ProxyAddr means loopback, and a zero port means the
// SOCKS5 front-end is disabled (rendered as empty, never as ":0").
func resolverListenAddr(addr string, port uint) string {
	if port == 0 {
		return ""
	}
	if strings.TrimSpace(addr) == "" {
		addr = "127.0.0.1"
	}
	return fmt.Sprintf("%s:%d", addr, port)
}

// chainTargetState labels what the visor KNOWS sits behind a downstream SOCKS5
// address, so the status page can say "skysocks-client · running" instead of
// implying it probed the address. Returns "" when the address belongs to
// something this visor cannot vouch for — the page then shows the bare address.
//
// WHY no dial probe: the page renders on the request path of the very proxy the
// browser is talking through, and the js/wasm visor's chain rides a vnet where a
// raw TCP dial is meaningless. A claim derived from process state is honest in
// both builds; a half-second dial is neither cheap nor portable.
func (p *visorStatusProvider) chainTargetState(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if rt := p.v.embeddedSkynetWeb; rt != nil {
		if _, p2, e := net.SplitHostPort(rt.ListenAddr()); e == nil && p2 == port {
			return "skynet_web resolving proxy · " + runLabel(rt.IsRunning())
		}
	}
	if rt := p.v.embeddedDmsgWeb; rt != nil {
		if _, p2, e := net.SplitHostPort(rt.ListenAddr()); e == nil && p2 == port {
			return "dmsg_web resolving proxy · " + runLabel(rt.IsRunning())
		}
	}
	// skysocks-client binds SkysocksClientAddr (":1080"), so its port alone
	// identifies it; the client is a managed app, so procM knows its liveness.
	if _, p2, e := net.SplitHostPort("0.0.0.0" + skyenv.SkysocksClientAddr); e == nil && p2 == port {
		running := false
		if p.v.procM != nil {
			proc, ok := p.v.procM.ProcByName(skyenv.SkysocksClientName)
			running = ok && proc != nil
		}
		return "skysocks-client · " + runLabel(running)
	}
	return ""
}

// listForwardedPortsSafe is ListForwardedPorts with the registry-not-yet-built
// case folded in: a half-initialized visor reports "no forwards" instead of
// panicking on the status page's request path.
func (v *Visor) listForwardedPortsSafe() ([]ForwardedPort, error) {
	if v.forwardedPorts == nil {
		return nil, nil
	}
	return v.ListForwardedPorts()
}

func runLabel(running bool) string {
	if running {
		return "running"
	}
	return "stopped"
}

// layerFromStats projects a resolving proxy's own Stats counters into the shared
// proxystatus.Layer shape. Both resolvers keep structurally identical Stats, so
// the two callers differ only in the fields they add around this.
func layerFromStats(listen, suffix, upstream string, started time.Time, uptimeSec int64,
	total, ok, failed uint64, active int64, lastReq *time.Time, lastErr string) *proxystatus.Layer {
	l := &proxystatus.Layer{
		Listen:        listen,
		Suffix:        suffix,
		Upstream:      upstream,
		UptimeSec:     uptimeSec,
		Requests:      total,
		Successful:    ok,
		Failed:        failed,
		Active:        active,
		LastRequestAt: lastReq,
		LastError:     lastErr,
	}
	if !started.IsZero() {
		t := started
		l.StartedAt = &t
	}
	return l
}

// aliasList renders a resolver's name→PK map as a stable, name-sorted list of
// full (never truncated) keys.
func aliasList(m map[string]cipher.PubKey) []proxystatus.Alias {
	if len(m) == 0 {
		return nil
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]proxystatus.Alias, 0, len(names))
	for _, name := range names {
		out = append(out, proxystatus.Alias{Name: name, PK: m[name].String()})
	}
	return out
}

// statusFillBudget bounds each layer-section collector on the status page's
// request path. The page renders THROUGH the very proxy it describes, so a
// collector that blocks (on a lock, an RPC, a half-initialized registry) would
// otherwise hang the whole page forever — observed live on the browser visor:
// status.skynet never answered while the identical native path rendered in
// 15ms. Generous enough that a merely-busy visor still fills every section.
const statusFillBudget = 3 * time.Second

// fillLayerBounded runs fill against a SCRATCH snapshot under statusFillBudget
// and merges the layer sections into snap only on completion. On timeout the
// page renders without the section and the note NAMES it — turning a
// silent environment-specific wedge into a self-diagnosing line. The scratch
// snapshot is what makes the timeout safe: the abandoned goroutine keeps
// writing to memory nothing else reads (it is parked on whatever wedged and
// cannot be killed; one goroutine per timed-out page fetch, bounded by page
// traffic).
func (p *visorStatusProvider) fillLayerBounded(snap *proxystatus.Snapshot, name string, fill func(*proxystatus.Snapshot)) {
	scratch := &proxystatus.Snapshot{Surface: snap.Surface, App: snap.App, Running: snap.Running}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fill(scratch)
	}()
	select {
	case <-done:
		snap.Running = scratch.Running
		snap.Layer = scratch.Layer
		snap.Names = scratch.Names
		snap.Sessions = scratch.Sessions
		snap.Forwards = scratch.Forwards
		snap.Conns = scratch.Conns
		if scratch.Note != "" {
			snap.Note = appendNote(snap.Note, scratch.Note)
		}
	case <-time.After(statusFillBudget):
		snap.Note = appendNote(snap.Note, name+" collection timed out after "+statusFillBudget.String()+" — section omitted")
	}
}

// fillDmsgLayer populates the dmsg resolving proxy's sections: its layer summary
// (uptime + request counters from the runtime's own Stats, listener, suffix,
// downstream chain), the dmsg client's established server sessions, and the
// name-resolution state. Every value comes from something the layer already
// keeps — no lookup history is invented, because neither resolver caches one.
func (p *visorStatusProvider) fillDmsgLayer(snap *proxystatus.Snapshot) {
	rt := p.v.embeddedDmsgWeb
	if rt != nil {
		snap.Running = rt.IsRunning()
		st := rt.Stats()
		upstream := rt.Upstream()
		snap.Layer = layerFromStats(rt.ListenAddr(), rt.Suffix(), upstream,
			st.StartedAt, st.UptimeSec, st.TotalRequests, st.Successful, st.Failed,
			st.Active, st.LastRequestAt, st.LastError)
		snap.Layer.UpstreamState = p.chainTargetState(upstream)
		snap.Names = &proxystatus.Names{
			Kind:    "dmsg discovery (public key destinations) + configured aliases",
			Suffix:  rt.Suffix(),
			Aliases: aliasList(rt.Aliases()),
		}
	} else {
		// The runtime is not constructed (resolver disabled): still render a layer
		// section so the page says "stopped" rather than showing nothing at all.
		snap.Layer = &proxystatus.Layer{Suffix: dmsgweb.DefaultDomainSuffix}
		snap.Note = appendNote(snap.Note, "dmsg resolving proxy is not enabled on this visor")
	}

	// dmsg sessions are the dmsg layer's real transport state: without one, no
	// .dmsg name can resolve. Read from the visor's dmsg client directly.
	if p.v.dmsgC == nil {
		return
	}
	for _, ses := range p.v.dmsgC.AllSessions() {
		s := proxystatus.DmsgSession{
			ServerPK: ses.RemotePK().String(),
			Protocol: ses.Protocol(),
			Streams:  ses.NumStreams(),
		}
		if a := ses.RemoteTCPAddr(); a != nil {
			s.Addr = a.String()
		}
		if d := ses.LastPing(); d > 0 {
			s.PingMS = float64(d) / float64(time.Millisecond)
		}
		snap.Sessions = append(snap.Sessions, s)
	}
	sort.Slice(snap.Sessions, func(i, j int) bool { return snap.Sessions[i].ServerPK < snap.Sessions[j].ServerPK })
}

// fillSkynetLayer populates the skynet resolving proxy's sections: its layer
// summary and upstream chain (the skysocks-client it hands clearnet traffic to),
// the ports this visor forwards over skynet, and the raw forwarded conns open
// through the skynet plane right now.
func (p *visorStatusProvider) fillSkynetLayer(snap *proxystatus.Snapshot) {
	rt := p.v.embeddedSkynetWeb
	if rt != nil {
		snap.Running = rt.IsRunning()
		st := rt.Stats()
		upstream := rt.Upstream()
		snap.Layer = layerFromStats(rt.ListenAddr(), rt.Suffix(), upstream,
			st.StartedAt, st.UptimeSec, st.TotalRequests, st.Successful, st.Failed,
			st.Active, st.LastRequestAt, st.LastError)
		snap.Layer.UpstreamState = p.chainTargetState(upstream)
		snap.Names = &proxystatus.Names{
			Kind:    "skynet route plane (public key destinations) + configured aliases",
			Suffix:  rt.Suffix(),
			Aliases: aliasList(rt.Aliases()),
		}
	} else {
		snap.Layer = &proxystatus.Layer{Suffix: skynetweb.DefaultDomainSuffix}
		snap.Note = appendNote(snap.Note, "skynet resolving proxy is not enabled on this visor")
	}

	// Forwarded ports: what this visor exposes to the mesh over skynet. Only the
	// skynet-plane entries belong on the skynet page.
	if fps, err := p.v.listForwardedPortsSafe(); err == nil {
		for _, f := range fps {
			if !f.Skynet {
				continue
			}
			snap.Forwards = append(snap.Forwards, proxystatus.Forward{
				Port:      f.Port,
				LocalPort: f.LocalPort,
				Label:     f.Label,
				Skynet:    f.Skynet,
				DMSG:      f.DMSG,
				UDP:       f.UDP,
			})
		}
		sort.Slice(snap.Forwards, func(i, j int) bool { return snap.Forwards[i].Port < snap.Forwards[j].Port })
	} else {
		snap.Note = appendNote(snap.Note, "forwarded ports unavailable: "+err.Error())
	}

	// Active raw forwarded conns through the skynet plane, with their metered
	// port pairs — the conn-level view standing in for route-group legs.
	if conns, err := p.v.ListRawTCP(); err == nil {
		for id, c := range conns {
			if c == nil || !strings.EqualFold(c.Network, "skynet") {
				continue
			}
			snap.Conns = append(snap.Conns, proxystatus.Conn{
				ID:         id.String(),
				Network:    c.Network,
				LocalPort:  c.LocalPort,
				RemotePort: c.RemotePort,
			})
		}
		sort.Slice(snap.Conns, func(i, j int) bool { return snap.Conns[i].ID < snap.Conns[j].ID })
	}
}

func appendNote(existing, add string) string {
	if existing == "" {
		return add
	}
	return existing + " · " + add
}

// proxyLegFrom transcribes one visor MuxLegInfo into the proxystatus.Leg shape
// the status tree renders. Shared by every tunnel's leg list.
func proxyLegFrom(leg MuxLegInfo) proxystatus.Leg {
	return proxystatus.Leg{
		Index:          leg.Index,
		TransportID:    leg.TransportID,
		TpType:         leg.TpType,
		RemotePK:       leg.RemotePK,
		LatencyMS:      leg.LatencyMS,
		RouteLatencyMS: leg.RouteLatencyMS,
		Direct:         leg.Direct,
		SentBytes:      leg.SentBytes,
		RecvBytes:      leg.RecvBytes,
		DupBytes:       leg.DupBytes,
		RepairBytes:    leg.RepairBytes,
		Retransmits:    leg.Retransmits,
		GoodputBps:     leg.GoodputBps,
		GoodputUpBps:   leg.GoodputUpBps,
		GoodputDownBps: leg.GoodputDownBps,
		Alive:          leg.Alive,
		Standby:        leg.Standby,
		Hops:           proxyHopsFrom(leg.Hops),
	}
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
