// Package api pkg/dmsg/discovery/api/charts.go c4-net-discovery
package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// Series keys dmsg-discovery samples.
const (
	chartClients       = "clients"
	chartVisorClients  = "clients.visor"
	chartServers       = "servers"
	chartServersAvail  = "servers.avail"
	chartServerClients = "srv."
)

// StartCharts samples the discovery's counts into st until ctx ends and
// serves them as the charts page.
func (a *API) StartCharts(ctx context.Context, st charts.Store, log logrus.FieldLogger) {
	page := &charts.Page{
		Title: "Skywire dmsg discovery",
		About: strings.Split(a.dmsgAddr, ":")[0],
		Build: func(ctx context.Context, r charts.Range, now time.Time) (charts.Content, error) {
			return a.buildCharts(ctx, st, r, now)
		},
	}
	a.chartsPage.Store(page)
	go charts.Run(ctx, st, a.collectCharts, log)
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
	servers, err := a.db.AllServers(ctx)
	if err != nil {
		return nil, err
	}
	clients, err := a.getCachedAllClientEntries(ctx)
	if err != nil {
		return nil, err
	}
	v := map[string]float64{chartServers: float64(len(servers))}
	for _, s := range servers {
		if s.Server != nil && s.Server.AvailableSessions > 0 {
			v[chartServersAvail]++
		}
		v[chartServerClients+s.Static.Hex()] = 0
	}
	for _, c := range clients {
		if c == nil || c.Client == nil {
			continue
		}
		v[chartClients]++
		if c.ClientType == "visor" {
			v[chartVisorClients]++
		}
		for _, s := range c.Client.DelegatedServers {
			v[chartServerClients+s.Hex()]++
		}
	}
	return v, nil
}

func (a *API) buildCharts(ctx context.Context, st charts.Store, r charts.Range, now time.Time) (charts.Content, error) {
	f, from, err := r.Frame(ctx, st, now)
	if err != nil {
		return charts.Content{}, err
	}
	servers, err := a.db.AllServers(ctx)
	if err != nil {
		servers = nil
	}
	addr := map[string]*disc.Entry{}
	for _, s := range servers {
		addr[s.Static.Hex()] = s
	}
	var perServer []charts.Series
	for _, k := range f.Keys(chartServerClients) {
		pk := strings.TrimPrefix(k, chartServerClients)
		name := pk
		if e := addr[pk]; e != nil && e.Server != nil && e.Server.Address != "" {
			name = e.Server.Address
		}
		perServer = append(perServer, charts.Series{Name: name, Title: pk, Vals: f.Values(k, false)})
	}
	out := charts.Content{Charts: []charts.Chart{
		{Title: "dmsg clients", Note: "Live client entries, and those registered by visors.", Kind: charts.Lines,
			From: from, To: now, Times: f.Times, Series: []charts.Series{
				{Name: "clients", Vals: f.Values(chartClients, false)},
				{Name: "visors", Vals: f.Values(chartVisorClients, false)},
			}},
		{Title: "dmsg servers", Note: "Registered dmsg servers, and those with free sessions.", Kind: charts.Lines,
			From: from, To: now, Times: f.Times, Series: []charts.Series{
				{Name: "registered", Vals: f.Values(chartServers, false)},
				{Name: "with free sessions", Vals: f.Values(chartServersAvail, false)},
			}},
		{Title: "Clients per dmsg server", Note: "A client delegates to several servers, so these add up to more than the client count.",
			Kind: charts.Lines, From: from, To: now, Times: f.Times, Series: perServer},
	}}
	out.Tables = append(out.Tables, a.serverTable(f, servers))
	return out, nil
}

func (a *API) serverTable(f *charts.Frame, servers []*disc.Entry) charts.Table {
	t := charts.Table{Title: "dmsg servers now", Head: []string{"Public key", "Address", "Clients", "Free sessions", "Type", "Official"}}
	sort.Slice(servers, func(i, j int) bool {
		ci, _ := f.Latest(chartServerClients + servers[i].Static.Hex())
		cj, _ := f.Latest(chartServerClients + servers[j].Static.Hex())
		return ci > cj
	})
	for _, s := range servers {
		if s.Server == nil {
			continue
		}
		pk := s.Static.Hex()
		clients := "-"
		if n, ok := f.Latest(chartServerClients + pk); ok {
			clients = strconv.FormatFloat(n, 'f', 0, 64)
		}
		official := ""
		if a.OfficialServers[pk] {
			official = "yes"
		}
		t.Rows = append(t.Rows, []string{pk, s.Server.Address, clients, strconv.Itoa(s.Server.AvailableSessions), s.Server.ServerType, official})
	}
	return t
}
