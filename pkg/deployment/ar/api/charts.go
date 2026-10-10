// Package api pkg/deployment/ar/api/charts.go
package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/deployment/charts"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// Series keys the address resolver samples. Lookups and binds are counted
// per sample interval.
const (
	chartPeers        = "peers"
	chartLive         = "udp.live"
	chartBound        = "bound."
	chartOpen         = "open."
	chartClosed       = "closed."
	chartFound        = "found."
	chartNotFound     = "notfound."
	chartBinds        = "binds."
	chartBindHTTP     = "src.http."
	chartBindCXO      = "src.cxo."
	chartBindUDP      = "src.udp"
	chartListRequests = "req.transports"
	chartLookupUDP    = "req.udplookup"
	resolveFound      = "found"
	resolveNotFound   = "notfound"
)

// arCounters counts lookups and binds since the resolver started.
type arCounters struct {
	mu sync.Mutex
	n  map[string]uint64
}

func (c *arCounters) add(key string) {
	c.mu.Lock()
	if c.n == nil {
		c.n = map[string]uint64{}
	}
	c.n[key]++
	c.mu.Unlock()
}

func (c *arCounters) snapshot() map[string]uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]uint64, len(c.n))
	for k, v := range c.n {
		out[k] = v
	}
	return out
}

func typeName(t string) string { return string(types.NormalizeType(types.Type(t))) }

func (a *API) countResolve(t, outcome string) { a.counters.add(outcome + "." + typeName(t)) }

func (a *API) countBind(t types.Type) { a.counters.add(chartBinds + typeName(string(t))) }

// StartCharts samples the resolver's bindings and lookups into st until ctx
// ends and serves them as the charts page.
func (a *API) StartCharts(ctx context.Context, st charts.Store, log logrus.FieldLogger) {
	page := &charts.Page{
		Title: "Skywire address resolver",
		About: strings.Split(a.dmsgAddr, ":")[0],
		Stats: a.Stats,
		Store: st,
		Log:   log,
		Build: func(ctx context.Context, r charts.Range, now time.Time) (charts.Content, error) {
			return buildCharts(ctx, st, r, now)
		},
	}
	a.chartsPage.Store(page)
	var prev map[string]uint64
	go charts.Run(ctx, st, a.Stats.Wrap(func(context.Context) (map[string]float64, error) {
		v := a.reachSample()
		cur := a.counters.snapshot()
		for k, n := range cur {
			v[k] = float64(n - prev[k])
		}
		prev = cur
		return v, nil
	}), log)
}

func (a *API) reachSample() map[string]float64 {
	c := a.reach.counts()
	v := map[string]float64{chartPeers: float64(c.peers), chartLive: float64(c.live)}
	for t, n := range c.bound {
		v[chartBound+t] = float64(n)
	}
	for t, n := range c.open {
		v[chartOpen+t] = float64(n)
	}
	for t, n := range c.closed {
		v[chartClosed+t] = float64(n)
	}
	return v
}

// ChartsPage serves the charts page, or 404 when charts are not running.
func (a *API) ChartsPage(w http.ResponseWriter, r *http.Request) {
	p := a.chartsPage.Load()
	if p == nil {
		http.NotFound(w, r)
		return
	}
	p.ServeHTTP(w, r)
}

func buildCharts(ctx context.Context, st charts.Store, r charts.Range, now time.Time) (charts.Content, error) {
	f, from, err := r.Frame(ctx, st, now)
	if err != nil {
		return charts.Content{}, err
	}
	chart := func(title, note string, kind charts.Kind, series []charts.Series) charts.Chart {
		return charts.Chart{Title: title, Note: note, Kind: kind, From: from, To: now, Times: f.Times, Series: series}
	}

	bound := f.Group(chartBound, 0)
	bound = append(bound, charts.Series{Name: "sudph control link", Vals: f.Values(chartLive, false)})

	var open []charts.Series
	for _, s := range f.Group(chartOpen, 0) {
		b, _ := f.Latest(chartBound + s.Name)
		o, _ := f.Latest(chartOpen + s.Name)
		c, _ := f.Latest(chartClosed + s.Name)
		share := "-"
		if o+c > 0 {
			share = strconv.FormatFloat(100*o/(o+c), 'f', 0, 64) + "%"
		}
		s.Cells = []string{fmtCount(b), fmtCount(c), share}
		open = append(open, s)
	}
	reach := chart("Visors reachable from the internet, by type",
		"Bindings that answered when the resolver dialed them back. Most visors are behind NAT and do not.", charts.Lines, open)
	reach.Legend = []string{"Type", "Bound", "Did not answer", "Answered share", "Answered"}

	found := f.Group(chartFound, 0)
	notFound := f.Group(chartNotFound, 0)
	lookups := []charts.Series{
		{Name: "found", Vals: sumSeries(found, len(f.Times))},
		{Name: "not found", Vals: sumSeries(notFound, len(f.Times))},
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].Name < found[j].Name })

	return charts.Content{Charts: []charts.Chart{
		chart("Visors with a binding, by type",
			"Visors that told the resolver where they can be dialed, and those holding a sudph control link.", charts.Lines, bound),
		reach,
		chart("Requests", "Requests visors made to the address resolver per 5 minutes: lookups, binds over HTTP and over the sudph control link, and fetches of every peer's advertised types.",
			charts.Lines, []charts.Series{
				{Name: "lookups", Vals: sumSeries(append(append([]charts.Series{}, found...), notFound...), len(f.Times))},
				{Name: "binds over HTTP", Vals: sumSeries(f.Group(chartBindHTTP, 0), len(f.Times))},
				{Name: "sudph binds over UDP", Vals: f.Values(chartBindUDP, true)},
				{Name: "sudph lookups over UDP", Vals: f.Values(chartLookupUDP, true)},
				{Name: "full lists", Vals: f.Values(chartListRequests, true)},
			}),
		chart("Lookups", "Address lookups per 5 minutes, and how many found a binding.", charts.Lines, lookups),
		chart("Lookups that found a binding, by type", "Successful lookups per 5 minutes.", charts.Stacked, found),
		chart("Lookups that found nothing, by type", "Lookups of a peer with no binding of that type, per 5 minutes.", charts.Stacked, notFound),
		chart("Bindings written, by source", "Store writes per 5 minutes: HTTP binds, refreshes from visors' CXO feeds, and sudph binds over UDP.",
			charts.Stacked, []charts.Series{
				{Name: "HTTP", Vals: sumSeries(f.Group(chartBindHTTP, 0), len(f.Times))},
				{Name: "CXO feed", Vals: sumSeries(f.Group(chartBindCXO, 0), len(f.Times))},
				{Name: "sudph UDP", Vals: f.Values(chartBindUDP, true)},
			}),
	}}, nil
}

func sumSeries(in []charts.Series, n int) []float64 {
	out := make([]float64, n)
	for _, s := range in {
		for i, v := range s.Vals {
			out[i] += v
		}
	}
	return out
}

func fmtCount(v float64) string { return strconv.FormatFloat(v, 'f', 0, 64) }

// Charts is the charts page, or nil until StartCharts runs.
func (a *API) Charts() *charts.Page {
	return a.chartsPage.Load()
}
