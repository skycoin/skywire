// Package visor pkg/visor/hypervisor_handlers_ui2_services.go c3-vis-core
package visor

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The deployment services page: the health the visor sees for each
// service, a service's /health payload, and the route setup nodes' stats.

func (hv *Hypervisor) ui2ServicesRoutes(r *httputil.Router) {
	r.Get("/services", hv.ui2Services(false))
	r.Get("/services/body", hv.ui2Services(true))
	r.Get("/services/drill", hv.ui2ServiceDrill())
	r.Get("/services/rsn", hv.ui2RSN())
}

func (hv *Hypervisor) ui2Services(body bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(httpTimeout + 5*time.Second)) //nolint:errcheck
		s := ui2.Services{Checked: time.Now().Format(time.TimeOnly)}
		if hv.visor == nil {
			s.Err = "visor unavailable"
		} else if es, err := hv.visor.ServiceHealth(); err != nil {
			s.Err = err.Error()
		} else {
			s.Entries = es
		}
		if body {
			hv.render(w, r, ui2.ServicesBody(s))
			return
		}
		hv.render(w, r, ui2.ServicesPage(s))
	}
}

// ui2ServiceDrill shows a service's /health payload, indented when it is
// JSON, through the same fetch as /api/svc-fetch.
func (hv *Hypervisor) ui2ServiceDrill() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(httpTimeout + 5*time.Second)) //nolint:errcheck
		svc := r.URL.Query().Get("service")
		if hv.visor == nil {
			hv.render(w, r, ui2.ServiceDrill(svc, "", "visor unavailable"))
			return
		}
		b, err := hv.visor.fetchServiceDataCtx(r.Context(), svc, "/health")
		if err != nil {
			hv.render(w, r, ui2.ServiceDrill(svc, "", err.Error()))
			return
		}
		var out bytes.Buffer
		if json.Indent(&out, b, "", "  ") != nil {
			out.Reset()
			out.Write(b)
		}
		hv.render(w, r, ui2.ServiceDrill(svc, out.String(), ""))
	}
}

func (hv *Hypervisor) ui2RSN() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(rsnRemoteStatsTimeout + 5*time.Second)) //nolint:errcheck
		if hv.visor == nil {
			hv.render(w, r, ui2.RSNStats(nil))
			return
		}
		stats := hv.rsnRemoteStats()
		rows := make([]ui2.RSNRow, 0, len(stats))
		for _, s := range stats {
			row := ui2.RSNRow{PK: s.PK.Hex(), Err: s.Error}
			if sn := s.Snapshot; sn != nil {
				row.HasStats = true
				row.Rate, row.Successful, row.Failed, row.Active = sn.SuccessRatePct, sn.Successful, sn.Failed, sn.ActiveRequests
				row.P50, row.P95, row.P99 = sn.LatencyMs.P50, sn.LatencyMs.P95, sn.LatencyMs.P99
				if sn.LastSuccessAt != nil && !sn.LastSuccessAt.IsZero() {
					row.LastSuccess = sn.LastSuccessAt.Local().Format(time.TimeOnly)
				}
				if sn.LastFailureAt != nil && !sn.LastFailureAt.IsZero() {
					row.LastFailure = sn.LastFailureAt.Local().Format(time.TimeOnly)
				}
				fails := map[string]uint64{}
				for k, n := range sn.FailuresByReason {
					fails[string(k)] = n
				}
				row.Failures = ui2.TopFailures(fails)
			} else if row.Err == "" && s.Status != 0 {
				row.Err = http.StatusText(s.Status)
			}
			rows = append(rows, row)
		}
		hv.render(w, r, ui2.RSNStats(rows))
	}
}
