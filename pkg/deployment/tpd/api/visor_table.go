// Package api pkg/deployment/tpd/api/visor_table.go c4-net-discovery
package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/charts"
)

// VisorsPage serves every visor's transports by type, as of the last refresh
// of TPD's transport list.
func (api *API) VisorsPage(w http.ResponseWriter, r *http.Request) {
	c := api.chartState.Load()
	if c == nil || c.visors == nil {
		http.NotFound(w, r)
		return
	}
	c.visors.ServeHTTP(w, r)
}

// visorTable counts each visor's live transports by type. Online visors
// without any transport are listed too, at the end.
func (api *API) visorTable(ctx context.Context) charts.Table {
	entries := api.getTransportsFromCache(true)
	type row struct {
		pk     cipher.PubKey
		total  int
		byType map[string]int
	}
	rows := map[cipher.PubKey]*row{}
	typeTotals := map[string]int{}
	tps := 0
	for _, e := range entries {
		if e.Edges[0] == e.Edges[1] {
			continue
		}
		tps++
		typeTotals[string(e.Type)]++
		for _, pk := range e.Edges {
			r := rows[pk]
			if r == nil {
				r = &row{pk: pk, byType: map[string]int{}}
				rows[pk] = r
			}
			r.total++
			r.byType[string(e.Type)]++
		}
	}
	online := map[cipher.PubKey]bool{}
	for _, u := range api.getUptimesFromCache() {
		if u.Online {
			online[u.PK] = true
			if rows[u.PK] == nil {
				rows[u.PK] = &row{pk: u.PK, byType: map[string]int{}}
			}
		}
	}
	types := make([]string, 0, len(typeTotals))
	for t := range typeTotals {
		types = append(types, t)
	}
	sort.Slice(types, func(i, j int) bool { return typeTotals[types[i]] > typeTotals[types[j]] })
	list := make([]*row, 0, len(rows))
	for _, r := range rows {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].total != list[j].total {
			return list[i].total > list[j].total
		}
		return list[i].pk.Hex() < list[j].pk.Hex()
	})

	roles := api.serverRoles(ctx)
	t := charts.Table{
		Title: "Transports by visor",
		Note: fmt.Sprintf("%d visors with %d transports, %d of them online. A transport counts for both its visors. Visors that run a dmsg server are tinted, blue when it is registered and green when it serves a hypervisor's LAN.",
			len(list), tps, len(online)),
		Head: append([]string{"Public key", "Role", "Online", "Total"}, types...),
	}
	for _, r := range list {
		pk := r.pk.Hex()
		on := ""
		if online[r.pk] {
			on = "yes"
		}
		cells := []string{pk, roleName[roles[pk]], on, strconv.Itoa(r.total)}
		for _, typ := range types {
			n := ""
			if v := r.byType[typ]; v > 0 {
				n = strconv.Itoa(v)
			}
			cells = append(cells, n)
		}
		t.Rows = append(t.Rows, cells)
		t.Marks = append(t.Marks, roleColor[roles[pk]])
	}
	return t
}

// visorsPage is the page visorTable is served on.
func (api *API) visorsPage(about string) *charts.Page {
	return &charts.Page{
		Title:    "Skywire transports by visor",
		About:    about,
		Links:    []charts.Link{{Name: "Status", Href: "./"}, {Name: "Network graph", Href: "graph"}},
		NoRanges: true,
		Build: func(ctx context.Context, _ charts.Range, _ time.Time) (charts.Content, error) {
			return charts.Content{Tables: []charts.Table{api.visorTable(ctx)}}, nil
		},
	}
}
