// Package api pkg/deployment/rf/api/charts.go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
)

// Series keys the route finder samples. Counts are per sample interval.
const (
	chartRequests    = "rf.requests"
	chartPairs       = "rf.pairs"
	chartNoRoute     = "rf.noroute"
	chartFailed      = "rf.failed"
	chartMeanMs      = "rf.ms"
	chartP50Ms       = "rf.p50"
	chartP95Ms       = "rf.p95"
	chartHopsPrefix  = "rf.hops."
	chartFoundPrefix = "rf.found."
	chartGraphVisors = "graph.visors"
	chartGraphTps    = "graph.transports"
	chartSNPrefix    = "sn."
	chartSNFail      = "snfail."
	chartSNHops      = "snhops."
)

// SetupNodes are the route setup nodes whose public /stats the route finder
// charts, read over Client (an HTTP client that dials dmsg).
type SetupNodes struct {
	PKs    []cipher.PubKey
	Client *http.Client
	Port   uint16
}

const setupNodeTimeout = 30 * time.Second

type rfCharts struct {
	sn *SetupNodes

	mu       sync.Mutex
	prev     *requestStats
	prevNode map[cipher.PubKey]*setupmetrics.StatsSnapshot
}

// StartCharts samples the finder's counts, and those of sn when it is set,
// into st until ctx ends and serves them as the charts page.
func (a *API) StartCharts(ctx context.Context, st charts.Store, sn *SetupNodes, log logrus.FieldLogger) {
	c := &rfCharts{sn: sn, prevNode: map[cipher.PubKey]*setupmetrics.StatsSnapshot{}}
	page := &charts.Page{
		Title: "Skywire route finder",
		About: strings.Split(a.dmsgAddr, ":")[0],
		Stats: a.Stats,
		Store: st,
		Log:   log,
		Build: func(ctx context.Context, r charts.Range, now time.Time) (charts.Content, error) {
			return a.buildCharts(ctx, st, c, r, now)
		},
	}
	a.chartsPage.Store(page)
	go charts.Run(ctx, st, a.Stats.Wrap(func(ctx context.Context) (map[string]float64, error) { return a.collectCharts(ctx, c) }), log)
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

func (a *API) collectCharts(ctx context.Context, c *rfCharts) (map[string]float64, error) {
	v := map[string]float64{}
	cur := a.stats.snapshot()
	c.mu.Lock()
	prev := c.prev
	c.prev = &cur
	c.mu.Unlock()
	if prev == nil {
		prev = &requestStats{}
	}
	req := cur.requests - prev.requests
	v[chartRequests] = float64(req)
	v[chartPairs] = float64(cur.pairs - prev.pairs)
	v[chartNoRoute] = float64(cur.noRoute - prev.noRoute)
	v[chartFailed] = float64(cur.failed - prev.failed)
	if req > 0 {
		v[chartMeanMs] = float64((cur.elapsed-prev.elapsed)/time.Millisecond) / float64(req)
	}
	if t := a.stats.takeTimes(); len(t) > 0 {
		v[chartP50Ms] = t[len(t)/2]
		v[chartP95Ms] = t[len(t)*95/100]
	}
	for h := 1; h <= maxCountedHops; h++ {
		v[chartHopsPrefix+strconv.Itoa(h)] = float64(cur.hops[h] - prev.hops[h])
	}
	for n := 1; n <= maxCountedRoutes; n++ {
		v[chartFoundPrefix+strconv.Itoa(n)] = float64(cur.perPair[n] - prev.perPair[n])
	}
	if a.graphCache != nil {
		if g := a.graphCache.Get(); g != nil {
			visors, tps := g.Size()
			v[chartGraphVisors] = float64(visors)
			v[chartGraphTps] = float64(tps)
		}
	}
	if c.sn != nil {
		c.collectSetupNodes(ctx, v)
	}
	return v, nil
}

// collectSetupNodes reads every setup node's /stats at once and records the
// change since the previous read. A node that restarted starts over.
func (c *rfCharts) collectSetupNodes(ctx context.Context, v map[string]float64) {
	snaps := make([]*setupmetrics.StatsSnapshot, len(c.sn.PKs))
	var wg sync.WaitGroup
	for i, pk := range c.sn.PKs {
		wg.Add(1)
		go func(i int, pk cipher.PubKey) {
			defer wg.Done()
			snaps[i] = c.fetchSetupNode(ctx, pk)
		}(i, pk)
	}
	wg.Wait()

	for i, pk := range c.sn.PKs {
		k := chartSNPrefix + pk.Hex() + "."
		s := snaps[i]
		if s == nil {
			v[k+"up"] = 0
			continue
		}
		v[k+"up"] = 1
		if s.LatencyMs.Count > 0 {
			v[k+"p50"] = float64(s.LatencyMs.P50)
			v[k+"p95"] = float64(s.LatencyMs.P95)
		}
		c.mu.Lock()
		prev := c.prevNode[pk]
		c.prevNode[pk] = s
		c.mu.Unlock()
		if prev == nil {
			continue
		}
		if !prev.StartedAt.Equal(s.StartedAt) || s.TotalRequests < prev.TotalRequests {
			prev = &setupmetrics.StatsSnapshot{}
		}
		ok, failed := s.Successful-prev.Successful, s.Failed-prev.Failed
		v[k+"req"] = float64(s.TotalRequests - prev.TotalRequests)
		v[k+"drops"] = float64(s.ConcurrencyDrops - prev.ConcurrencyDrops)
		if ok+failed > 0 {
			v[k+"rate"] = 100 * float64(ok) / float64(ok+failed)
		}
		for reason, n := range s.FailuresByReason {
			v[chartSNFail+string(reason)] += float64(n - prev.FailuresByReason[reason])
		}
		for hops, n := range s.RouteLengthHist {
			b := hops
			if b > maxCountedHops {
				b = maxCountedHops
			}
			v[chartSNHops+strconv.Itoa(b)] += float64(n - prev.RouteLengthHist[hops])
		}
	}
}

func (c *rfCharts) fetchSetupNode(ctx context.Context, pk cipher.PubKey) *setupmetrics.StatsSnapshot {
	ctx, cancel := context.WithTimeout(ctx, setupNodeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s:%d/stats", pk.Hex(), c.sn.Port), nil)
	if err != nil {
		return nil
	}
	resp, err := c.sn.Client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var s setupmetrics.StatsSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil
	}
	return &s
}

func hopName(h int) string {
	switch {
	case h == 1:
		return "1 hop"
	case h == maxCountedHops:
		return strconv.Itoa(h) + "+ hops"
	default:
		return strconv.Itoa(h) + " hops"
	}
}

func hopSeries(f *charts.Frame, prefix string) []charts.Series {
	var out []charts.Series
	for h := 1; h <= maxCountedHops; h++ {
		out = append(out, charts.Series{Name: hopName(h), Vals: f.Values(prefix+strconv.Itoa(h), true)})
	}
	return out
}

func (a *API) buildCharts(ctx context.Context, st charts.Store, c *rfCharts, r charts.Range, now time.Time) (charts.Content, error) {
	f, from, err := r.Frame(ctx, st, now)
	if err != nil {
		return charts.Content{}, err
	}
	ms := func(v float64) string { return strconv.FormatFloat(v, 'f', 0, 64) + " ms" }
	pct := func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) + "%" }
	chart := func(title, note string, kind charts.Kind, series []charts.Series) charts.Chart {
		return charts.Chart{Title: title, Note: note, Kind: kind, From: from, To: now, Times: f.Times, Series: series}
	}

	out := charts.Content{Charts: []charts.Chart{
		chart("Route requests", "Requests per 5 minutes, the source and destination pairs they asked for, and pairs with no route.",
			charts.Lines, []charts.Series{
				{Name: "requests", Vals: f.Values(chartRequests, false)},
				{Name: "pairs", Vals: f.Values(chartPairs, false)},
				{Name: "no route", Vals: f.Values(chartNoRoute, false)},
				{Name: "errors", Vals: f.Values(chartFailed, false)},
			}),
		chart("Routes found per pair", "How many routes each pair asked for got back, per 5 minutes. Mux dials ask for several.", charts.Stacked, foundSeries(f)),
		chart("Length of routes found", "Hops in every route returned, per 5 minutes.", charts.Stacked, hopSeries(f, chartHopsPrefix)),
		chart("Route graph", "Visors and transports in the graph routes are found in.", charts.Lines, []charts.Series{
			{Name: "visors", Vals: f.Values(chartGraphVisors, false)},
			{Name: "transports", Vals: f.Values(chartGraphTps, false)},
		}),
	}}
	mean := chart("Route search time", "Time from a route request's arrival to its answer: the median, the 95th percentile and the mean per 5 minutes.", charts.Lines,
		[]charts.Series{
			{Name: "median", Vals: f.Values(chartP50Ms, false)},
			{Name: "95th percentile", Vals: f.Values(chartP95Ms, false)},
			{Name: "mean", Vals: f.Values(chartMeanMs, false)},
		})
	mean.Format = ms
	out.Charts = append(out.Charts, mean)

	if c.sn == nil {
		return out, nil
	}
	var reqs, rates, lat []charts.Series
	for _, pk := range c.sn.PKs {
		k := chartSNPrefix + pk.Hex() + "."
		up, _ := f.Latest(k + "up")
		state := "answering"
		if up == 0 {
			state = "not answering"
		}
		rate := "-"
		if v, ok := f.Latest(k + "rate"); ok {
			rate = pct(v)
		}
		reqs = append(reqs, charts.Series{Name: pk.Hex(), Vals: f.Values(k+"req", false), Cells: []string{state, rate}})
		rates = append(rates, charts.Series{Name: pk.Hex(), Vals: f.Values(k+"rate", false)})
		lat = append(lat,
			charts.Series{Name: pk.Hex() + " p50", Vals: f.Values(k+"p50", false)},
			charts.Series{Name: pk.Hex() + " p95", Vals: f.Values(k+"p95", false)})
	}
	sn := chart("Route setup requests", "Setups each route setup node handled, per 5 minutes.", charts.Lines, reqs)
	sn.Legend = []string{"Setup node", "Now", "Success rate", "Requests"}
	rt := chart("Route setup success rate", "Successful setups as a share of finished ones.", charts.Lines, rates)
	rt.Format = pct
	rt.Legend = []string{"Setup node", "Success rate"}
	lt := chart("Route setup latency", "Median and 95th percentile time of successful setups.", charts.Lines, lat)
	lt.Format = ms
	lt.Legend = []string{"Setup node and percentile", "Latency"}
	failures := f.Group(chartSNFail, 0)
	sort.SliceStable(failures, func(i, j int) bool { return failures[i].Name < failures[j].Name })
	out.Charts = append(out.Charts, sn, rt, lt,
		chart("Route setup failures by reason", "Failed setups per 5 minutes, across all setup nodes.", charts.Stacked, failures),
		chart("Length of routes set up", "Hops in each successful setup, across all setup nodes.", charts.Stacked, hopSeries(f, chartSNHops)))
	return out, nil
}

func foundSeries(f *charts.Frame) []charts.Series {
	var out []charts.Series
	for n := 1; n <= maxCountedRoutes; n++ {
		name := strconv.Itoa(n) + " routes"
		switch n {
		case 1:
			name = "1 route"
		case maxCountedRoutes:
			name = strconv.Itoa(n) + "+ routes"
		}
		out = append(out, charts.Series{Name: name, Vals: f.Values(chartFoundPrefix+strconv.Itoa(n), true)})
	}
	return out
}

// Charts is the charts page, or nil until StartCharts runs.
func (a *API) Charts() *charts.Page {
	return a.chartsPage.Load()
}
