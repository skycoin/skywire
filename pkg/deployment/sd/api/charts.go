// Package api pkg/deployment/sd/api/charts.go
package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

// Series keys service discovery samples.
const (
	chartTypePrefix    = "type."
	chartVPNCountry    = "vpn.cc."
	chartProxyCountry  = "proxy.cc."
	chartVersionPrefix = "ver."
)

// chartTypes are the service types sampled. Proxy and skysocks are one kind
// of service registered under two names, so they are counted together.
var chartTypes = []string{servicedisc.ServiceTypeVisor, servicedisc.ServiceTypeVPN,
	servicedisc.ServiceTypeSkysocks, servicedisc.ServiceTypeProxy, servicedisc.ServiceTypeCoin}

// StartCharts samples the registered services into st until ctx ends and
// serves them as the charts page.
func (a *API) StartCharts(ctx context.Context, st charts.Store, log logrus.FieldLogger) {
	page := &charts.Page{
		Title: "Skywire service discovery",
		About: strings.Split(a.dmsgAddr, ":")[0],
		Stats: a.Stats,
		Store: st,
		Log:   log,
		Build: func(ctx context.Context, r charts.Range, now time.Time) (charts.Content, error) {
			return buildCharts(ctx, st, r, now)
		},
	}
	a.chartsPage.Store(page)
	go charts.Run(ctx, st, a.Stats.Wrap(a.collectCharts), log)
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

func (a *API) collectCharts(ctx context.Context) (map[string]float64, error) {
	v := map[string]float64{}
	for _, t := range chartTypes {
		services, herr := a.db.Services(ctx, t, "", "")
		if herr != nil {
			return nil, errors.New(herr.Error())
		}
		name := t
		if t == servicedisc.ServiceTypeSkysocks {
			name = servicedisc.ServiceTypeProxy
		}
		v[chartTypePrefix+name] += float64(len(services))
		for _, s := range services {
			ver := s.Version
			if ver == "" {
				ver = "unknown"
			}
			v[chartVersionPrefix+ver]++
			cc := "unknown"
			if s.Geo != nil && s.Geo.Country != "" {
				cc = s.Geo.Country
			}
			switch name {
			case servicedisc.ServiceTypeVPN:
				v[chartVPNCountry+cc]++
			case servicedisc.ServiceTypeProxy:
				v[chartProxyCountry+cc]++
			}
		}
	}
	return v, nil
}

func buildCharts(ctx context.Context, st charts.Store, r charts.Range, now time.Time) (charts.Content, error) {
	f, from, err := r.Frame(ctx, st, now)
	if err != nil {
		return charts.Content{}, err
	}
	chart := func(title, note string, series []charts.Series) charts.Chart {
		return charts.Chart{Title: title, Note: note, Kind: charts.Stacked, From: from, To: now, Times: f.Times, Series: series}
	}
	return charts.Content{Charts: []charts.Chart{
		chart("Services by type", "Registered services. Proxy counts proxy and skysocks entries, which are the same service.",
			f.Group(chartTypePrefix, 0)),
		chart("VPN servers by country", "Registered VPN servers by the country in their entry, ten largest.", f.Group(chartVPNCountry, 10)),
		chart("Proxy servers by country", "Registered proxy servers by the country in their entry, ten largest.", f.Group(chartProxyCountry, 10)),
		chart("Service versions", "Registered services by the visor version they report, eight largest.", f.Group(chartVersionPrefix, 8)),
	}}, nil
}

// Charts is the charts page, or nil until StartCharts runs.
func (a *API) Charts() *charts.Page {
	return a.chartsPage.Load()
}
