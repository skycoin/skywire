package visor

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/stats"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// Series keys of the visor's own charts. Byte counts are per sample interval.
const (
	chartTpCount   = "tp.count."
	chartTpIn      = "tp.in."
	chartTpOut     = "tp.out."
	chartRoutes    = "routes"
	chartDmsgSess  = "dmsg.sessions"
	chartAppsAlive = "apps.running"

	// Bytes both ways on the busiest transports, keyed "<type> <remote pk>",
	// the rest summed under chartTpTopOther, and the latency of each in ms.
	chartTpTop      = "tpx."
	chartTpTopOther = "tpx-other"
	chartTpLat      = "tplat."

	chartAppOut = "app.out."
	chartAppIn  = "app.in."

	chartMuxGroups      = "mux.groups"
	chartMuxStandby     = "mux.standby"
	chartMuxLegs        = "mux.legs"
	chartMuxStandbyLegs = "mux.standby.legs"
	chartMuxEvent       = "mux.ev."
	chartMuxLegOut      = "mux.leg.out."
	chartMuxLegIn       = "mux.leg.in."
)

// topTransports is how many transports get a series of their own.
const topTransports = 8

// unattributed names route group traffic no app can be found for.
const unattributed = "visor"

// visorDmsgPorts names the dmsg ports a visor listens on, for the page's
// traffic by port chart.
var visorDmsgPorts = map[uint16]string{
	visorconfig.DmsgHTTPPort:            "log server",
	skyenv.DmsgCtrlPort:                 "dmsg ctrl",
	skyenv.DmsgPingPort:                 "dmsg ping",
	skyenv.DmsgPtyPort:                  "pty",
	skyenv.DmsgSetupPort:                "route setup node",
	skyenv.TransportPort:                "dmsg transports",
	skyenv.DmsgHypervisorPort:           "hypervisor",
	skyenv.DmsgTransportSetupPort:       "transport setup",
	skyenv.DmsgGRPCPort:                 "gRPC (gotop, stats)",
	skyenv.DmsgCXOPort:                  "telemetry feed",
	skyenv.DmsgWebRTCSignalPort:         "WebRTC signaling",
	skyenv.SkychatVoiceSignalPort:       "skychat voice signaling",
	skyenv.SkychatVoiceMediaPort:        "skychat voice media",
	skyenv.SkychatFilePort:              "skychat files",
	skyenv.SkychatGroupProbePort:        "skychat group probe",
	skyenv.DmsgVisorRPCPort:             "visor RPC",
	skyenv.DmsgDMSGDRegistrationCXOPort: "dmsg discovery registration feed",
	skyenv.DmsgTransportQueryPort:       "transport query",
	skyenv.DmsgVisorTPListCXOPort:       "transport list feed",
	skyenv.DmsgRelayPort:                "dmsg relay",
	skyenv.DmsgVisorARBindCXOPort:       "address resolver bind feed",
	skyenv.DmsgVisorSDRegCXOPort:        "service discovery registration feed",
	skyenv.DmsgConfCXOPort:              "services config feed",
	skyenv.DmsgDHTPort:                  "DHT",
	skyenv.DmsgAwaitSetupPort:           "route setup",
}

// visorCharts samples the visor's load and traffic for its private status
// page, keeping the history in the stats store when there is one.
type visorCharts struct {
	v    *Visor
	page *charts.Page

	mu      sync.Mutex
	prev    map[uuid.UUID]transport.BandwidthData
	prevRG  map[string][2]uint64
	prevLeg map[string][2]uint64
	prevMux router.MuxCounters
}

// startVisorCharts samples into st, or into memory when st is nil, until the
// visor's context ends.
func startVisorCharts(v *Visor, st *stats.Store, log *logging.Logger) {
	store := charts.NewMemoryStore()
	if st != nil {
		bs, err := charts.NewBboltStore(st.DB(), "charts")
		if err != nil {
			log.WithError(err).Warn("Charts: keeping history in memory")
		} else {
			store = bs
		}
	}
	ss := charts.NewServiceStats("visor")
	if v.dmsgC != nil {
		ss.CountDmsg(v.dmsgC)
		for port, name := range visorDmsgPorts {
			ss.NamePort(port, name)
		}
	}
	c := &visorCharts{v: v}
	c.page = &charts.Page{
		Title: "Skywire visor",
		About: v.conf.PK.Hex(),
		Stats: ss,
		Store: store,
		Log:   log,
		Build: func(ctx context.Context, r charts.Range, now time.Time) (charts.Content, error) {
			return buildVisorCharts(ctx, store, r, now)
		},
	}
	v.visorCharts.Store(c)
	go charts.Run(v.ctx, store, ss.Wrap(c.collect), log)
}

// tpSample is one open transport as the collector reads it.
type tpSample struct {
	id      uuid.UUID
	typ     string
	remote  cipher.PubKey
	latency float64
	bw      transport.BandwidthData
}

func (c *visorCharts) collect(context.Context) (map[string]float64, error) {
	v := c.v
	out := map[string]float64{}
	c.mu.Lock()
	defer c.mu.Unlock()
	if v.tpM != nil {
		var tps []tpSample
		v.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
			if !tp.IsClosed() {
				tps = append(tps, tpSample{id: tp.Entry.ID, typ: string(tp.Entry.Type), remote: tp.Remote(), latency: tp.GetLatency(), bw: *tp.GetBandwidth()})
			}
			return true
		})
		c.sampleTransports(out, tps)
	}
	if v.router != nil {
		out[chartRoutes] = float64(v.router.RoutesCount())
		appByPort := func(routing.Port) (string, bool) { return "", false }
		if v.procM != nil {
			appByPort = v.procM.AppByPort
		}
		infos := v.router.RouteGroupMuxInfoAll()
		c.sampleRouteGroups(out, v.router.ActiveRouteStatuses(), infos, appByPort)
		c.sampleMux(out, infos, v.router.MuxCounters())
	}
	if v.dmsgC != nil {
		out[chartDmsgSess] = float64(len(v.dmsgC.AllSessions()))
	}
	if v.appL != nil {
		n := 0
		for _, st := range v.appL.AppStates() {
			if st != nil && st.Status == appserver.AppStatusRunning {
				n++
			}
		}
		out[chartAppsAlive] = float64(n)
	}
	return out, nil
}

// delta is cur less prev, or all of cur when the counter restarted.
func delta(cur, prev uint64) float64 {
	if cur < prev {
		return float64(cur)
	}
	return float64(cur - prev)
}

// sampleTransports adds the by-type totals and the busiest transports.
func (c *visorCharts) sampleTransports(out map[string]float64, tps []tpSample) {
	cur := make(map[uuid.UUID]transport.BandwidthData, len(tps))
	busy := map[string]float64{}
	lat := map[string]float64{}
	for _, tp := range tps {
		cur[tp.id] = tp.bw
		// A transport new since the last sample counts from zero.
		p := c.prev[tp.id]
		if tp.bw.SentBytes < p.SentBytes || tp.bw.RecvBytes < p.RecvBytes {
			p = transport.BandwidthData{}
		}
		sent, recv := float64(tp.bw.SentBytes-p.SentBytes), float64(tp.bw.RecvBytes-p.RecvBytes)
		out[chartTpCount+tp.typ]++
		out[chartTpOut+tp.typ] += sent
		out[chartTpIn+tp.typ] += recv
		name := tp.typ + " " + tp.remote.Hex()
		busy[name] += sent + recv
		lat[name] = math.Max(lat[name], tp.latency)
	}
	c.prev = cur
	names := make([]string, 0, len(busy))
	for n := range busy {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if busy[names[i]] != busy[names[j]] {
			return busy[names[i]] > busy[names[j]]
		}
		return names[i] < names[j]
	})
	for i, n := range names {
		if i >= topTransports {
			out[chartTpTopOther] += busy[n]
			continue
		}
		out[chartTpTop+n] = busy[n]
		out[chartTpLat+n] = lat[n]
	}
}

func descKey(srcPK cipher.PubKey, srcPort routing.Port, dstPK cipher.PubKey, dstPort routing.Port) string {
	return srcPK.Hex() + ":" + strconv.Itoa(int(srcPort)) + "-" + dstPK.Hex() + ":" + strconv.Itoa(int(dstPort))
}

func infoKey(info router.MuxInfo) string {
	d := info.Desc
	return descKey(d.SrcPK(), d.SrcPort(), d.DstPK(), d.DstPort())
}

// sampleRouteGroups adds the bytes each app moved over its own route groups,
// counted on the group rather than on the transports it shares.
func (c *visorCharts) sampleRouteGroups(out map[string]float64, sts []router.RouteStatus, infos []router.MuxInfo, appByPort func(routing.Port) (string, bool)) {
	dialer := make(map[string]string, len(infos))
	for _, info := range infos {
		if info.AppName != "" {
			dialer[infoKey(info)] = info.AppName
		}
	}
	cur := make(map[string][2]uint64, len(sts))
	for _, st := range sts {
		k := descKey(st.RemotePK, st.RemotePort, st.LocalPK, st.LocalPort)
		now := [2]uint64{st.BandwidthSent, st.BandwidthReceived}
		cur[k] = now
		p := c.prevRG[k]
		app := dialer[k]
		if app == "" {
			app, _ = appByPort(st.LocalPort)
		}
		if app == "" {
			app = unattributed
		}
		out[chartAppOut+app] += delta(now[0], p[0])
		out[chartAppIn+app] += delta(now[1], p[1])
	}
	c.prevRG = cur
}

// sampleMux adds route group and leg counts, the failover counters' change
// since the last sample, and leg traffic by direct or multihop leg.
func (c *visorCharts) sampleMux(out map[string]float64, infos []router.MuxInfo, mc router.MuxCounters) {
	out[chartMuxGroups] = float64(len(infos))
	out[chartMuxStandby] = 0
	out[chartMuxLegs] = 0
	out[chartMuxStandbyLegs] = 0
	cur := map[string][2]uint64{}
	for _, info := range infos {
		out[chartMuxLegs] += float64(len(info.Legs))
		if info.TunnelRole == "standby" {
			out[chartMuxStandby]++
			out[chartMuxStandbyLegs] += float64(len(info.Legs))
		}
		if !info.MuxEnabled {
			continue
		}
		gk := infoKey(info)
		for _, leg := range info.Legs {
			k := gk + "|" + leg.TransportID
			now := [2]uint64{leg.SentBytes, leg.RecvBytes}
			cur[k] = now
			p := c.prevLeg[k]
			kind := "multihop"
			if leg.Direct {
				kind = "direct"
			}
			out[chartMuxLegOut+kind] += delta(now[0], p[0])
			out[chartMuxLegIn+kind] += delta(now[1], p[1])
		}
	}
	c.prevLeg = cur
	p := c.prevMux
	for k, d := range map[string][2]uint64{
		"promotions":      {mc.TunnelPromotions, p.TunnelPromotions},
		"rehome.sent":     {mc.LegRehomesSent, p.LegRehomesSent},
		"rehome.acked":    {mc.LegRehomesAcked, p.LegRehomesAcked},
		"rehome.failed":   {mc.LegRehomesFailed, p.LegRehomesFailed},
		"rehome.received": {mc.LegRehomesReceived, p.LegRehomesReceived},
		"split.sent":      {mc.LegSplitsSent, p.LegSplitsSent},
		"split.acked":     {mc.LegSplitsAcked, p.LegSplitsAcked},
		"split.failed":    {mc.LegSplitsFailed, p.LegSplitsFailed},
		"split.received":  {mc.LegSplitsReceived, p.LegSplitsReceived},
	} {
		out[chartMuxEvent+k] = delta(d[0], d[1])
	}
	c.prevMux = mc
}

// muxEventNames labels the failover counters in chart order.
var muxEventNames = []struct{ key, name string }{
	{"promotions", "standby tunnel promoted"},
	{"rehome.sent", "leg re-home sent"},
	{"rehome.acked", "leg re-home acked"},
	{"rehome.failed", "leg re-home failed"},
	{"rehome.received", "leg re-home received"},
	{"split.sent", "leg split sent"},
	{"split.acked", "leg split acked"},
	{"split.failed", "leg split failed"},
	{"split.received", "leg split received"},
}

// busiestTransports draws the per-transport series, with the collector's
// own remainder folded into the "other" series and latency in the legend.
func busiestTransports(f *charts.Frame) []charts.Series {
	series := f.Group(chartTpTop, topTransports)
	other := f.Values(chartTpTopOther, true)
	merged := false
	for i := range series {
		if series[i].Name == "other" {
			for j, v := range other {
				series[i].Vals[j] += v
			}
			series[i].Cells = []string{""}
			merged = true
			continue
		}
		cell := ""
		if l, ok := f.Latest(chartTpLat + series[i].Name); ok {
			cell = fmt.Sprintf("%.0f ms", l)
		}
		series[i].Cells = []string{cell}
	}
	if !merged {
		for _, v := range other {
			if v > 0 {
				series = append(series, charts.Series{Name: "other", Cells: []string{""}, Vals: other})
				break
			}
		}
	}
	return series
}

func buildVisorCharts(ctx context.Context, st charts.Store, r charts.Range, now time.Time) (charts.Content, error) {
	f, from, err := r.Frame(ctx, st, now)
	if err != nil {
		return charts.Content{}, err
	}
	chart := func(title, note string, kind charts.Kind, format func(float64) string, series []charts.Series) charts.Chart {
		return charts.Chart{Title: title, Note: note, Kind: kind, From: from, To: now, Times: f.Times, Format: format, Series: series}
	}
	events := make([]charts.Series, 0, len(muxEventNames))
	for _, e := range muxEventNames {
		events = append(events, charts.Series{Name: e.name, Vals: f.Values(chartMuxEvent+e.key, true)})
	}
	busiest := chart("Busiest transports", "Bytes sent and received per 5 minutes on the transports that carried the most, by type and remote key, with each one's latest latency.", charts.Stacked, charts.Bytes, busiestTransports(f))
	busiest.Legend = []string{"Transport", "Latency", "Latest"}
	return charts.Content{Charts: []charts.Chart{
		chart("Bytes sent over transports, by type", "What this visor sent per 5 minutes, by transport type.", charts.Stacked, charts.Bytes, f.Group(chartTpOut, 0)),
		chart("Bytes received over transports, by type", "What this visor received per 5 minutes, by transport type.", charts.Stacked, charts.Bytes, f.Group(chartTpIn, 0)),
		busiest,
		chart("Transports, by type", "Open transports at each sample.", charts.Stacked, nil, f.Group(chartTpCount, 0)),
		chart("Bytes sent by apps", "What each app sent per 5 minutes over its own routes, counted on the route rather than the shared transport. Visor is routes no app owns.", charts.Stacked, charts.Bytes, f.Group(chartAppOut, 0)),
		chart("Bytes received by apps", "What each app received per 5 minutes over its own routes.", charts.Stacked, charts.Bytes, f.Group(chartAppIn, 0)),
		chart("Route groups and mux legs", "Route groups and the transport legs they ride at each sample, and how many of each are standby tunnels held ready to take over.", charts.Lines, nil, []charts.Series{
			{Name: "route groups", Vals: f.Values(chartMuxGroups, true)},
			{Name: "legs", Vals: f.Values(chartMuxLegs, true)},
			{Name: "standby tunnels", Vals: f.Values(chartMuxStandby, true)},
			{Name: "standby legs", Vals: f.Values(chartMuxStandbyLegs, true)},
		}),
		chart("Mux failover events", "Standby tunnel promotions and legs moved into or split out of a route group per 5 minutes. Spikes are the mux working around a failing path.", charts.Lines, nil, events),
		chart("Mux leg traffic, direct or multihop", "Bytes per 5 minutes on multiplexed route legs, split by legs that reach the peer directly and legs through other visors.", charts.Stacked, charts.Bytes, []charts.Series{
			{Name: "direct sent", Vals: f.Values(chartMuxLegOut+"direct", true)},
			{Name: "direct received", Vals: f.Values(chartMuxLegIn+"direct", true)},
			{Name: "multihop sent", Vals: f.Values(chartMuxLegOut+"multihop", true)},
			{Name: "multihop received", Vals: f.Values(chartMuxLegIn+"multihop", true)},
		}),
		chart("Routes and sessions", "Routing rules, dmsg sessions and running apps at each sample.", charts.Lines, nil, []charts.Series{
			{Name: "routing rules", Vals: f.Values(chartRoutes, false)},
			{Name: "dmsg sessions", Vals: f.Values(chartDmsgSess, false)},
			{Name: "apps running", Vals: f.Values(chartAppsAlive, true)},
		}),
	}}, nil
}

// serveVisorStatus serves the visor's status page, or 404 before it starts.
func (v *Visor) serveVisorStatus(w http.ResponseWriter, r *http.Request) {
	c := v.visorCharts.Load()
	if c == nil {
		http.NotFound(w, r)
		return
	}
	c.page.ServeHTTP(w, r)
}

// VisorCharts returns the samples and starts of the visor's status page in
// the window args names.
func (v *Visor) VisorCharts(args visorapi.VisorChartsArgs) (*visorapi.VisorCharts, error) {
	c := v.visorCharts.Load()
	if c == nil {
		return nil, errors.New("visor charts not started")
	}
	ctx := context.Background()
	samples, err := c.page.Store.Range(ctx, args.From, args.To, args.Hourly)
	if err != nil {
		return nil, err
	}
	starts, err := c.page.Store.Starts(ctx, args.From, args.To)
	if err != nil {
		return nil, err
	}
	out := &visorapi.VisorCharts{}
	for _, s := range samples {
		out.Samples = append(out.Samples, visorapi.ChartSample{At: s.At, V: s.V})
	}
	for _, s := range starts {
		out.Starts = append(out.Starts, visorapi.ChartStart{At: s.At, Version: s.Version, Commit: s.Commit})
	}
	return out, nil
}
