package visor

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/logging"
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
)

// visorCharts samples the visor's load and traffic for its private status
// page, keeping the history in the stats store when there is one.
type visorCharts struct {
	v    *Visor
	page *charts.Page

	mu   sync.Mutex
	prev map[uuid.UUID]transport.BandwidthData
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
		ss.NamePort(visorconfig.DmsgHTTPPort, "log server")
	}
	c := &visorCharts{v: v, prev: map[uuid.UUID]transport.BandwidthData{}}
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

func (c *visorCharts) collect(context.Context) (map[string]float64, error) {
	v := c.v
	out := map[string]float64{}
	if v.tpM != nil {
		cur := map[uuid.UUID]transport.BandwidthData{}
		c.mu.Lock()
		v.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
			if tp.IsClosed() {
				return true
			}
			typ := string(tp.Entry.Type)
			bw := *tp.GetBandwidth()
			cur[tp.Entry.ID] = bw
			// A transport new since the last sample counts from zero.
			p := c.prev[tp.Entry.ID]
			if bw.SentBytes < p.SentBytes || bw.RecvBytes < p.RecvBytes {
				p = transport.BandwidthData{}
			}
			out[chartTpCount+typ]++
			out[chartTpOut+typ] += float64(bw.SentBytes - p.SentBytes)
			out[chartTpIn+typ] += float64(bw.RecvBytes - p.RecvBytes)
			return true
		})
		c.prev = cur
		c.mu.Unlock()
	}
	if v.router != nil {
		out[chartRoutes] = float64(v.router.RoutesCount())
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

func buildVisorCharts(ctx context.Context, st charts.Store, r charts.Range, now time.Time) (charts.Content, error) {
	f, from, err := r.Frame(ctx, st, now)
	if err != nil {
		return charts.Content{}, err
	}
	chart := func(title, note string, kind charts.Kind, format func(float64) string, series []charts.Series) charts.Chart {
		return charts.Chart{Title: title, Note: note, Kind: kind, From: from, To: now, Times: f.Times, Format: format, Series: series}
	}
	return charts.Content{Charts: []charts.Chart{
		chart("Bytes sent over transports, by type", "What this visor sent per 5 minutes, by transport type.", charts.Stacked, charts.Bytes, f.Group(chartTpOut, 0)),
		chart("Bytes received over transports, by type", "What this visor received per 5 minutes, by transport type.", charts.Stacked, charts.Bytes, f.Group(chartTpIn, 0)),
		chart("Transports, by type", "Open transports at each sample.", charts.Stacked, nil, f.Group(chartTpCount, 0)),
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
