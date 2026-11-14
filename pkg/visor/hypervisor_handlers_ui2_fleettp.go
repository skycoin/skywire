// Package visor pkg/visor/hypervisor_handlers_ui2_fleettp.go c3-vis-core
package visor

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The transports of the whole network, read from /api/network/transports.

func (hv *Hypervisor) ui2FleetTpsRoutes(r *httputil.Router) {
	r.Get("/transports", hv.ui2FleetTps(false))
	r.Get("/transports/body", hv.ui2FleetTps(true))
}

func (hv *Hypervisor) ui2FleetTps(body bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(90 * time.Second)) //nolint:errcheck
		q := r.URL.Query()
		f := ui2.FleetTps{Days: 7, Tree: q.Get("tree") != "", HideEdges: q.Get("hide_edges") != "", LiveOnly: q.Get("live") != ""}
		switch d, _ := strconv.Atoi(q.Get("days")); d { //nolint:errcheck
		case 1, 30:
			f.Days = d
		}
		r2 := r.Clone(r.Context())
		q2 := r2.URL.Query()
		q2.Set("days", strconv.Itoa(f.Days))
		q2.Del("live")
		if f.LiveOnly {
			q2.Set("live", "true")
		}
		r2.URL.RawQuery = q2.Encode()
		cw := &captureWriter{h: http.Header{}, code: http.StatusOK}
		hv.getNetworkTransports().ServeHTTP(cw, r2)
		var m compactTransportMetrics
		var tps []ui2.FleetTp
		switch {
		case cw.code != http.StatusOK:
			f.Err = cw.errText()
		case json.Unmarshal(cw.body.Bytes(), &m) != nil:
			f.Err = "bad transport metrics"
		default:
			f.Total, f.Returned, f.LiveTotal, f.Bandwidth, f.Partial = m.Total, m.Returned, m.Live, m.NetworkBandwidth, m.Partial
			for _, row := range m.Metrics {
				t := ui2.FleetTp{ID: row.ID, Type: row.Type, Live: row.Live, Sent: row.Sent, Recv: row.Recv}
				if len(row.Edges) > 1 {
					t.A, t.B = row.Edges[0], row.Edges[1]
				}
				if row.Latency != nil {
					t.LatAvg = row.Latency.Avg
				}
				tps = append(tps, t)
			}
		}
		f = ui2.FleetTpsOf(f, tps, ui2.ParseListQuery(r.FormValue))
		f.Fetched = time.Now().Format(time.TimeOnly)
		if body {
			hv.render(w, r, ui2.FleetTpsBody(f))
			return
		}
		hv.render(w, r, ui2.FleetTpsPage(f))
	}
}
