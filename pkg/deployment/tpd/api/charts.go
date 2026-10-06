// Package api pkg/deployment/tpd/api/charts.go c4-net-discovery
package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
)

// Series keys TPD samples.
const (
	chartTransportPrefix = "tp."
	chartVersionPrefix   = "ver."
	chartVisorsLinked    = "visors.tp"
	chartVisorsOnline    = "visors.online"
)

const dailyChartTTL = 10 * time.Minute

type tpdCharts struct {
	st   charts.Store
	page *charts.Page

	mu      sync.Mutex
	daily   []store.DailyAggregate
	dailyAt time.Time
}

// StartCharts samples TPD's counts into st until ctx ends and serves them
// as the charts page.
func (api *API) StartCharts(ctx context.Context, st charts.Store, log logrus.FieldLogger) {
	c := &tpdCharts{st: st}
	c.page = &charts.Page{
		Title: "Skywire transport discovery",
		About: strings.Split(api.dmsgAddr, ":")[0],
		Build: func(ctx context.Context, r charts.Range, now time.Time) (charts.Content, error) {
			return api.buildCharts(ctx, c, r, now)
		},
	}
	api.chartState.Store(c)
	go charts.Run(ctx, st, api.collectCharts, log)
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
		visors := make(map[cipher.PubKey]struct{})
		for _, e := range entries {
			v[chartTransportPrefix+string(e.Type)]++
			for _, edge := range e.Edges {
				visors[edge] = struct{}{}
			}
		}
		v[chartVisorsLinked] = float64(len(visors))
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
		{Title: "Visor versions", Note: "Online visors by the version they report.",
			Kind: charts.Stacked, From: from, To: now, Times: f.Times, Series: f.Group(chartVersionPrefix, 8)},
	}}
	if daily := c.dailyAggregate(ctx, api.store); len(daily) > 0 {
		out.Charts = append(out.Charts, dailyBandwidthChart(daily))
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
	resp, err := st.GetNetworkMetrics(ctx, store.MetricsQuery{Days: statsDailyDays, Live: "all", Bandwidth: true})
	if err != nil || resp == nil {
		return c.daily
	}
	c.daily, c.dailyAt = resp.Daily, time.Now()
	return c.daily
}

func dailyBandwidthChart(daily []store.DailyAggregate) charts.Chart {
	var days []store.DailyAggregate
	var times []time.Time
	for _, d := range daily {
		if _, err := time.Parse("2006-01-02", d.Date); err == nil {
			days = append(days, d)
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })
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
