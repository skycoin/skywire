// Package visor pkg/visor/hypervisor_handlers_ui2_bandwidth.go c3-vis-core
package visor

import (
	"net/http"
	"strconv"
	"time"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The bandwidth tab: the visor's own per transport traffic store.

func (hv *Hypervisor) ui2BandwidthRoutes(r *httputil.Router) {
	r.Get("/ui2/visors/{pk}/bandwidth", hv.ui2Bandwidth(""))
	r.Get("/ui2/visors/{pk}/bandwidth/body", hv.ui2Bandwidth("body"))
	r.Get("/ui2/visors/{pk}/bandwidth/tp", hv.ui2Bandwidth("tp"))
}

func (hv *Hypervisor) ui2Bandwidth(op string) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		days := 7
		switch d, _ := strconv.Atoi(r.URL.Query().Get("days")); d { //nolint:errcheck
		case 1, 30:
			days = d
		}
		var b ui2.Bandwidth
		resp, err := ctx.API.LocalTransportStats()
		if err != nil || resp == nil {
			b = ui2.Bandwidth{PK: r.PathValue("pk"), Days: days}
			if err != nil {
				b.Err = err.Error()
			}
		} else {
			b = ui2.BandwidthOf(r.PathValue("pk"), days, resp.Transports, time.Now())
			b.Fetched = resp.FetchedAt.Local().Format(time.TimeOnly)
		}
		if op == "tp" {
			x, ok := b.Find(r.URL.Query().Get("id"))
			if !ok {
				hv.render(w, r, ui2.ErrorBox(b.Err))
				return
			}
			hv.render(w, r, ui2.BwDetail(x))
			return
		}
		b = b.Page(ui2.ParseListQuery(r.FormValue))
		if op == "body" {
			hv.render(w, r, ui2.BandwidthBody(b))
			return
		}
		hv.render(w, r, ui2.BandwidthPage(b))
	})
}
