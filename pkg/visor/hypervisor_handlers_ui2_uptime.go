// Package visor pkg/visor/hypervisor_handlers_ui2_uptime.go c3-vis-core
package visor

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// The uptime tab of the server rendered UI.

func (hv *Hypervisor) ui2UptimeRoutes(r *httputil.Router) {
	r.Get("/visors/{pk}/uptime", hv.ui2Uptime(false))
	r.Get("/visors/{pk}/uptime/bars", hv.ui2Uptime(true))
	r.Get("/visors/{pk}/uptime/services", hv.ui2ServiceUptimes())
}

// ui2Uptime serves the tab, or with bars set only the part that refreshes.
func (hv *Hypervisor) ui2Uptime(bars bool) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		u := ui2.Uptime{PK: r.PathValue("pk"), Days: 1}
		switch d, _ := strconv.Atoi(r.URL.Query().Get("days")); d { //nolint:errcheck
		case 7, 30:
			u.Days = d
		}
		now := time.Now()
		resp, err := ctx.API.LocalUptimeStats(visorapi.LocalUptimeArgs{Since: now.Add(-time.Duration(u.Days) * 24 * time.Hour)})
		if err != nil {
			u.Err = err.Error()
		} else {
			u.List, u.Summary = ui2.UptimeOf(resp, now)
			if !resp.FetchedAt.IsZero() {
				u.Fetched = resp.FetchedAt.Local().Format(time.TimeOnly)
			}
		}
		if bars {
			hv.render(w, r, ui2.UptimeBars(u))
			return
		}
		hv.render(w, r, ui2.UptimePage(u))
	})
}

// ui2ServiceUptimes asks TPD, SD and DMSG-D what they recorded of the visor,
// through this hypervisor's own visor as /api/svc-fetch does.
func (hv *Hypervisor) ui2ServiceUptimes() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var key cipher.PubKey
		if err := key.Set(r.PathValue("pk")); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		pk := key.Hex()
		rows := []ui2.ServiceUptime{{Service: "TPD"}, {Service: "SD"}, {Service: "DMSG-D"}}
		keys := []string{"tpd", "sd", "dmsgd"}
		var wg sync.WaitGroup
		for i := range rows {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if hv.visor == nil {
					rows[i].Err = "visor unavailable"
					return
				}
				body, err := hv.visor.fetchServiceDataCtx(r.Context(), keys[i], "/uptimes?v=v2&visors="+pk)
				if err == nil {
					rows[i].Days, err = ui2.ParseServiceUptimes(body, pk)
				}
				if err != nil {
					rows[i].Err = err.Error()
				}
			}(i)
		}
		wg.Wait()
		hv.render(w, r, ui2.ServiceUptimes(rows))
	}
}
