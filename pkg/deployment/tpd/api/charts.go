// Package api pkg/deployment/tpd/api/charts.go c4-net-discovery
package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/deployment/netgraph"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
)

// Series keys TPD samples.
const (
	chartTransportPrefix = "tp."
	chartVersionPrefix   = "ver."
	chartVisorsLinked    = "visors.tp"
	chartVisorsOnline    = "visors.online"
	chartPerVisorPrefix  = "pv."
)

const dailyChartTTL = 10 * time.Minute

type tpdCharts struct {
	st    charts.Store
	page  *charts.Page
	graph *netgraph.Page

	mu      sync.Mutex
	daily   []store.DailyAggregate
	dailyAt time.Time
	visorBW *store.VisorBWDay
}

// StartCharts samples TPD's counts into st until ctx ends and serves them
// as the charts page.
func (api *API) StartCharts(ctx context.Context, st charts.Store, log logrus.FieldLogger) {
	c := &tpdCharts{st: st}
	c.page = &charts.Page{
		Title: "Skywire transport discovery",
		About: strings.Split(api.dmsgAddr, ":")[0],
		Links: []charts.Link{{Name: "Network graph", Href: "graph"}},
		Stats: api.Stats,
		Store: st,
		Log:   log,
		Build: func(ctx context.Context, r charts.Range, now time.Time) (charts.Content, error) {
			return api.buildCharts(ctx, c, r, now)
		},
	}
	c.graph = &netgraph.Page{Title: "Skywire transport graph", Back: "./", Source: api.graphLinks, Marks: api.roleColors,
		Legend: []netgraph.Mark{{Name: roleName[roleRegistered], Color: roleColor[roleRegistered]}, {Name: roleName[roleLAN], Color: roleColor[roleLAN]}}}
	api.chartState.Store(c)
	go charts.Run(ctx, st, api.Stats.Wrap(api.collectCharts), log)
}

// ChartsPage serves the charts page, or 404 when charts are not running.
func (api *API) ChartsPage(w http.ResponseWriter, r *http.Request) {
	c := api.chartState.Load()
	if c == nil {
		http.NotFound(w, r)
		return
	}
	c.page.ServeHTTP(w, r)
}

// GraphPage serves the transport graph, or 404 when charts are not running.
func (api *API) GraphPage(w http.ResponseWriter, r *http.Request) {
	c := api.chartState.Load()
	if c == nil {
		http.NotFound(w, r)
		return
	}
	c.graph.ServeHTTP(w, r)
}

// graphLinks is every transport in the cache, for the transport graph.
func (api *API) graphLinks(context.Context) ([]netgraph.Link, error) {
	entries := api.getTransportsFromCache(true)
	if entries == nil {
		return nil, errors.New("transport cache is not warm yet")
	}
	links := make([]netgraph.Link, 0, len(entries))
	for _, e := range entries {
		links = append(links, netgraph.Link{A: e.Edges[0].Hex(), B: e.Edges[1].Hex(), Type: string(e.Type)})
	}
	return links, nil
}

func (api *API) collectCharts(ctx context.Context) (map[string]float64, error) {
	v := map[string]float64{}
	entries := api.getTransportsFromCache(true)
	if entries == nil {
		sum, err := api.store.GetTransportSummary(ctx, true)
		if err != nil {
			return nil, err
		}
		if sum.Partial {
			return nil, errors.New("transport summary is partial")
		}
		for t, n := range sum.ByType {
			v[chartTransportPrefix+t] = float64(n)
		}
		v[chartVisorsLinked] = float64(sum.UniqueVisors)
	} else {
		visors := make(map[cipher.PubKey]int)
		for _, e := range entries {
			v[chartTransportPrefix+string(e.Type)]++
			for _, edge := range e.Edges {
				visors[edge]++
			}
		}
		v[chartVisorsLinked] = float64(len(visors))
		for _, b := range perVisorBuckets {
			v[chartPerVisorPrefix+b.label] = 0
		}
		for _, n := range visors {
			v[chartPerVisorPrefix+perVisorBucket(n)]++
		}
	}
	if uptimes := api.getUptimesFromCache(); uptimes != nil {
		online := 0
		for _, u := range uptimes {
			if !u.Online {
				continue
			}
			online++
			ver := u.Version
			if ver == "" {
				ver = "unknown"
			}
			v[chartVersionPrefix+ver]++
		}
		v[chartVisorsOnline] = float64(online)
	}
	return v, nil
}

func (api *API) buildCharts(ctx context.Context, c *tpdCharts, r charts.Range, now time.Time) (charts.Content, error) {
	f, from, err := r.Frame(ctx, c.st, now)
	if err != nil {
		return charts.Content{}, err
	}
	visors := []charts.Series{
		{Name: "online", Vals: f.Values(chartVisorsOnline, false)},
		{Name: "with transports", Vals: f.Values(chartVisorsLinked, false)},
	}
	out := charts.Content{Charts: []charts.Chart{
		{Title: "Transports by type", Note: "Registered transports, stacked by type.",
			Kind: charts.Stacked, From: from, To: now, Times: f.Times, Series: f.Group(chartTransportPrefix, 0)},
		{Title: "Visors", Note: "Visors reporting online, and visors that are an edge of at least one transport.",
			Kind: charts.Lines, From: from, To: now, Times: f.Times, Series: visors},
		perVisorChart(f, from, now),
		{Title: "Visor versions", Note: "Online visors by the version they report.",
			Kind: charts.Stacked, From: from, To: now, Times: f.Times, Series: f.Group(chartVersionPrefix, 8)},
	}}
	if daily := c.dailyAggregate(ctx, api.store); len(daily) > 0 {
		out.Charts = append(out.Charts, dailyBandwidthChart(daily), dailyLatencyChart(daily))
	}
	out.Tables = append(out.Tables, api.visorTable(ctx))
	if t, ok := api.visorBandwidthTable(ctx, c, now); ok {
		out.Tables = append(out.Tables, t)
	}
	return out, nil
}

// dailyAggregate reads the 30-day aggregate at most every dailyChartTTL.
func (c *tpdCharts) dailyAggregate(ctx context.Context, st store.Store) []store.DailyAggregate {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.dailyAt) < dailyChartTTL {
		return c.daily
	}
	resp, err := st.GetNetworkMetrics(ctx, store.MetricsQuery{Days: statsDailyDays, Live: "all", Bandwidth: true, Latency: true})
	if err != nil || resp == nil {
		return c.daily
	}
	c.daily, c.dailyAt = resp.Daily, time.Now()
	return c.daily
}

func dailyBandwidthChart(daily []store.DailyAggregate) charts.Chart {
	days := sortedDays(daily)
	var times []time.Time
	byType := map[string][]float64{}
	for i, d := range days {
		t, _ := time.Parse("2006-01-02", d.Date) //nolint:errcheck
		times = append(times, t)
		for typ, agg := range d.ByType {
			if byType[typ] == nil {
				byType[typ] = make([]float64, len(days))
			}
			byType[typ][i] = float64(agg.Bandwidth)
		}
	}
	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)
	series := make([]charts.Series, 0, len(types))
	for _, t := range types {
		series = append(series, charts.Series{Name: t, Vals: byType[t]})
	}
	c := charts.Chart{Title: "Daily bandwidth by type", Note: "Bytes carried per UTC day over the last 30 days, whatever range is selected.",
		Kind: charts.Stacked, Times: times, Series: series, Format: charts.Bytes, Binary: true, Dates: true}
	if len(times) > 0 {
		c.From, c.To = times[0], times[len(times)-1]
	}
	return c
}

// perVisorBuckets are the reward site's buckets: one transport is no
// redundancy, two is some, and past five the count stops mattering.
var perVisorBuckets = []struct {
	label  string
	lo, hi int
}{{"1", 1, 1}, {"2", 2, 2}, {"3", 3, 3}, {"4", 4, 4}, {"5-9", 5, 9}, {"10+", 10, 0}}

func perVisorBucket(n int) string {
	for _, b := range perVisorBuckets {
		if n >= b.lo && (b.hi == 0 || n <= b.hi) {
			return b.label
		}
	}
	return perVisorBuckets[0].label
}

func perVisorChart(f *charts.Frame, from, to time.Time) charts.Chart {
	var series []charts.Series
	for _, b := range perVisorBuckets {
		name := b.label + " transports"
		if b.label == "1" {
			name = "1 transport"
		}
		series = append(series, charts.Series{Name: name, Vals: f.Values(chartPerVisorPrefix+b.label, false)})
	}
	return charts.Chart{Title: "Visors by transport count",
		Note: "How many transports each visor has, stacked. Visors with one transport have no redundancy.",
		Kind: charts.Stacked, From: from, To: to, Times: f.Times, Series: series}
}

func dailyLatencyChart(daily []store.DailyAggregate) charts.Chart {
	days := sortedDays(daily)
	all := make([]float64, len(days))
	byType := map[string][]float64{}
	var times []time.Time
	for i, d := range days {
		t, _ := time.Parse("2006-01-02", d.Date) //nolint:errcheck
		times = append(times, t)
		all[i] = msOrGap(d.Latency)
		for typ, agg := range d.ByType {
			if byType[typ] == nil {
				byType[typ] = make([]float64, len(days))
				for j := range byType[typ] {
					byType[typ][j] = math.NaN()
				}
			}
			byType[typ][i] = msOrGap(agg.Latency)
		}
	}
	series := []charts.Series{{Name: "all", Vals: all}}
	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		series = append(series, charts.Series{Name: t, Vals: byType[t]})
	}
	c := charts.Chart{Title: "Daily latency by type", Note: "Mean of each transport's average latency per UTC day, over the last 30 days.",
		Kind: charts.Lines, Times: times, Series: series, Dates: true,
		Format: func(v float64) string { return strconv.FormatFloat(v, 'f', 0, 64) + " ms" }}
	if len(times) > 0 {
		c.From, c.To = times[0], times[len(times)-1]
	}
	return c
}

// msOrGap keeps a day with no latency report as a gap rather than 0 ms.
func msOrGap(v float64) float64 {
	if v <= 0 {
		return math.NaN()
	}
	return v
}

func sortedDays(daily []store.DailyAggregate) []store.DailyAggregate {
	var days []store.DailyAggregate
	for _, d := range daily {
		if _, err := time.Parse("2006-01-02", d.Date); err == nil {
			days = append(days, d)
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })
	return days
}

const topVisorsByBandwidth = 25

// visorBandwidthTable lists the visors that sent the most on the last
// settled day, from the same per-visor totals rewards pool 2 pays from.
func (api *API) visorBandwidthTable(ctx context.Context, c *tpdCharts, now time.Time) (charts.Table, bool) {
	date := now.AddDate(0, 0, -1).Format("2006-01-02")
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.visorBW == nil || c.visorBW.Date != date {
		ls, ok := api.store.(leafStore)
		if !ok {
			return charts.Table{}, false
		}
		leaves, err := ls.LoadMetricsLeaves(ctx, []string{date})
		if err != nil || leaves[date] == nil {
			return charts.Table{}, false
		}
		records, err := decodeMetricsParts(leaves[date])
		if err != nil {
			return charts.Table{}, false
		}
		day := store.ComputeVisorBW(records, date)
		if len(day.Visors) == 0 {
			return charts.Table{}, false
		}
		c.visorBW = &day
	}
	return visorBWTable(c.visorBW, api.serverRoles(ctx)), true
}

func visorBWTable(day *store.VisorBWDay, roles map[string]string) charts.Table {
	type row struct {
		pk     string
		total  uint64
		byType map[string]uint64
	}
	rows := make([]row, 0, len(day.Visors))
	for pk, types := range day.Visors {
		r := row{pk: pk, byType: types}
		for _, n := range types {
			r.total += n
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].total != rows[j].total {
			return rows[i].total > rows[j].total
		}
		return rows[i].pk < rows[j].pk
	})
	t := charts.Table{Title: "Top visors by bytes sent, " + day.Date + " (UTC day)",
		Note: fmt.Sprintf("%d visors sent bytes over %d transports. %d transports between visors on the same network are left out, as in rewards pool 2. Visors that run a dmsg server are tinted, blue when it is registered in dmsg discovery and green when it serves a hypervisor's LAN.",
			len(day.Visors), day.Transports, day.SameNetworkExcluded),
		Head: []string{"Public key", "Role", "Sent", "By type"}}
	for i, r := range rows {
		if i == topVisorsByBandwidth {
			break
		}
		types := make([]string, 0, len(r.byType))
		for typ := range r.byType {
			types = append(types, typ)
		}
		sort.Slice(types, func(a, b int) bool { return r.byType[types[a]] > r.byType[types[b]] })
		parts := make([]string, 0, len(types))
		for _, typ := range types {
			parts = append(parts, typ+" "+charts.Bytes(float64(r.byType[typ])))
		}
		t.Rows = append(t.Rows, []string{r.pk, roleName[roles[r.pk]], charts.Bytes(float64(r.total)), strings.Join(parts, ", ")})
		t.Marks = append(t.Marks, roleColor[roles[r.pk]])
	}
	return t
}

// roleName and roleColor describe a dmsg server visor in tables and on the
// transport graph.
var (
	roleName  = map[string]string{roleRegistered: "registered dmsg server", roleLAN: "LAN dmsg server"}
	roleColor = map[string]string{roleRegistered: "#4e79a7", roleLAN: "#59a14f"}
)

// roleColors colors each dmsg server visor by its role, for the graph.
func (api *API) roleColors(ctx context.Context) map[string]string {
	roles := api.serverRoles(ctx)
	out := make(map[string]string, len(roles))
	for pk, r := range roles {
		out[pk] = roleColor[r]
	}
	return out
}

// Charts is the charts page, or nil until StartCharts runs.
func (api *API) Charts() *charts.Page {
	if c := api.chartState.Load(); c != nil {
		return c.page
	}
	return nil
}
