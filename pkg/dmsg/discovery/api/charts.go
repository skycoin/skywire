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

	"github.com/skycoin/skywire/pkg/cipher"
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
	chartServerFree    = "free."
	chartLANServers    = "lan.servers"
	chartLANClients    = "lan.clients"
)

// StartCharts samples the discovery's counts into st until ctx ends and
// serves them as the charts page.
func (a *API) StartCharts(ctx context.Context, st charts.Store, log logrus.FieldLogger) {
	page := &charts.Page{
		Title: "Skywire dmsg discovery",
		About: strings.Split(a.dmsgAddr, ":")[0],
		Stats: a.Stats,
		Store: st,
		Log:   log,
		Build: func(ctx context.Context, r charts.Range, now time.Time) (charts.Content, error) {
			return a.buildCharts(ctx, st, r, now)
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

// collectCharts counts clients per dmsg server. Servers clients delegate to
// without a discovery entry are the ones hypervisors run for their LAN.
func (a *API) collectCharts(ctx context.Context) (map[string]float64, error) {
	servers, err := a.db.AllServers(ctx)
	if err != nil {
		return nil, err
	}
	clients, err := a.getCachedAllClientEntries(ctx)
	if err != nil {
		return nil, err
	}
	registered := make(map[cipher.PubKey]bool, len(servers))
	v := map[string]float64{chartServers: float64(len(servers)), chartLANServers: 0, chartLANClients: 0}
	for _, s := range servers {
		if s.Server == nil {
			continue
		}
		registered[s.Static] = true
		if s.Server.AvailableSessions > 0 {
			v[chartServersAvail]++
		}
		v[chartServerClients+s.Static.Hex()] = 0
		v[chartServerFree+s.Static.Hex()] = float64(s.Server.AvailableSessions)
	}
	for _, c := range clients {
		if c == nil || c.Client == nil {
			continue
		}
		v[chartClients]++
		if c.ClientType == "visor" {
			v[chartVisorClients]++
		}
		lan := false
		for _, s := range c.Client.DelegatedServers {
			k := chartServerClients + s.Hex()
			if !registered[s] {
				lan = true
				if v[k] == 0 {
					v[chartLANServers]++
				}
			}
			v[k]++
		}
		if lan {
			v[chartLANClients]++
		}
	}
	if h := a.srvHealth.Load(); h != nil {
		pks := make([]cipher.PubKey, 0, len(registered))
		for pk := range registered {
			pks = append(pks, pk)
		}
		h.collect(ctx, pks, v)
	}
	return v, nil
}

// serverLoadCharts draws the load registered servers report on their /health.
func (a *API) serverLoadCharts(f *charts.Frame, from, now time.Time, reg map[string]*disc.Server) []charts.Chart {
	if len(f.Keys(chartSrvOK)) == 0 {
		return nil
	}
	var conns []charts.Series
	for _, k := range f.Keys(chartSrvConns) {
		pk := strings.TrimPrefix(k, chartSrvConns)
		addr := ""
		if s := reg[pk]; s != nil {
			addr = s.Address
		}
		peers, active := "-", "-"
		if n, ok := f.Latest(chartSrvPeers + pk); ok {
			peers = strconv.FormatFloat(n, 'f', 0, 64)
		}
		if n, ok := f.Latest(chartSrvActive + pk); ok {
			active = strconv.FormatFloat(n, 'f', 0, 64)
		}
		conns = append(conns, charts.Series{Name: pk, Vals: f.Values(k, false), Cells: []string{addr, peers, active}})
	}
	out := []charts.Chart{
		{Title: "Bytes relayed per dmsg server", Note: "What each registered server carried between its clients and peers per 5 minutes, both ways, as it reports on its /health.",
			Kind: charts.Stacked, Format: charts.Bytes, From: from, To: now, Times: f.Times, Series: f.Group(chartSrvBytes, 0)},
		{Title: "Streams relayed per dmsg server", Note: "Streams each registered server opened between two of its sessions per 5 minutes.",
			Kind: charts.Stacked, From: from, To: now, Times: f.Times, Series: f.Group(chartSrvStreams, 0)},
		{Title: "Sessions per dmsg server", Note: "Clients connected to each registered server, as the server counts them. Each is one connection, however many streams it carries.",
			Kind: charts.Lines, From: from, To: now, Times: f.Times, Series: conns,
			Legend: []string{"Public key", "Address", "Peer servers", "Active streams", "Sessions"}},
	}
	var silent []string
	for _, k := range f.Keys(chartSrvOK) {
		if n, ok := f.Latest(k); ok && n == 0 {
			silent = append(silent, strings.TrimPrefix(k, chartSrvOK))
		}
	}
	if len(silent) > 0 {
		sort.Strings(silent)
		out[0].Note += " Not reporting yet (older version, or no answer): " + strings.Join(silent, ", ") + "."
	}
	return out
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
	reg := map[string]*disc.Server{}
	for _, s := range servers {
		if s.Server != nil {
			reg[s.Static.Hex()] = s.Server
		}
	}

	var perServer, free []charts.Series
	type lanServer struct {
		pk      string
		clients float64
	}
	var lan []lanServer
	for _, k := range f.Keys(chartServerClients) {
		pk := strings.TrimPrefix(k, chartServerClients)
		srv := reg[pk]
		if srv == nil {
			if n, ok := f.Latest(k); ok && n > 0 {
				lan = append(lan, lanServer{pk, n})
			}
			continue
		}
		official := ""
		if a.OfficialServers[pk] {
			official = "yes"
		}
		perServer = append(perServer, charts.Series{Name: pk, Vals: f.Values(k, false),
			Cells: []string{srv.Address, srv.ServerType, official, strconv.Itoa(srv.AvailableSessions)}})
		free = append(free, charts.Series{Name: pk, Vals: f.Values(chartServerFree+pk, false), Cells: []string{srv.Address}})
	}

	out := charts.Content{Charts: []charts.Chart{
		{Title: "dmsg clients", Note: "Live client entries, and those registered by visors.", Kind: charts.Lines,
			From: from, To: now, Times: f.Times, Series: []charts.Series{
				{Name: "clients", Vals: f.Values(chartClients, false)},
				{Name: "visors", Vals: f.Values(chartVisorClients, false)},
			}},
		{Title: "Registered dmsg servers", Note: "Servers with a discovery entry, and those with free sessions.", Kind: charts.Lines,
			From: from, To: now, Times: f.Times, Series: []charts.Series{
				{Name: "registered", Vals: f.Values(chartServers, false)},
				{Name: "with free sessions", Vals: f.Values(chartServersAvail, false)},
			}},
		{Title: "Clients per registered dmsg server",
			Note: "A client delegates to several servers, so these add up to more than the client count.",
			Kind: charts.Lines, From: from, To: now, Times: f.Times, Series: perServer,
			Legend: []string{"Public key", "Address", "Type", "Official", "Free sessions", "Clients"}},
		{Title: "Free sessions per registered dmsg server", Note: "Spare capacity each server advertises in its entry.",
			Kind: charts.Lines, From: from, To: now, Times: f.Times, Series: free,
			Legend: []string{"Public key", "Address", "Free sessions"}},
		{Title: "Hypervisor LAN dmsg servers",
			Note: "Servers that hypervisors run for the visors on their LAN. They have no discovery entry, by design.",
			Kind: charts.Lines, From: from, To: now, Times: f.Times, Series: []charts.Series{
				{Name: "servers", Vals: f.Values(chartLANServers, false)},
				{Name: "clients using them", Vals: f.Values(chartLANClients, false)},
			}},
	}}
	out.Charts = append(out.Charts, a.serverLoadCharts(f, from, now, reg)...)
	sort.SliceStable(lan, func(i, j int) bool { return lan[i].clients > lan[j].clients })
	t := charts.Table{Title: "Hypervisor LAN dmsg servers now", Head: []string{"Public key", "Clients"}}
	for _, l := range lan {
		t.Rows = append(t.Rows, []string{l.pk, strconv.FormatFloat(l.clients, 'f', 0, 64)})
	}
	out.Tables = append(out.Tables, t)
	return out, nil
}

// Charts is the charts page, or nil until StartCharts runs.
func (a *API) Charts() *charts.Page {
	return a.chartsPage.Load()
}
