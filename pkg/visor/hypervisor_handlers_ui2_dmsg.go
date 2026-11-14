// Package visor pkg/visor/hypervisor_handlers_ui2_dmsg.go c3-vis-core
package visor

import (
	"net/http"
	"strconv"
	"time"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The dmsg tab of the server rendered UI.

func (hv *Hypervisor) ui2DmsgRoutes(r *httputil.Router) {
	r.Get("/visors/{pk}/dmsg", hv.ui2Dmsg(false))
	r.Get("/visors/{pk}/dmsg/clients", hv.ui2Dmsg(true))
	r.Post("/visors/{pk}/dmsg/connect-all", hv.ui2DmsgConnectAll())
	r.Put("/visors/{pk}/dmsg/sessions-count", hv.ui2DmsgSessionsCount())
}

func ui2DmsgState(pk string, ctx *httpCtx) ui2.Dmsg {
	d := ui2.Dmsg{PK: pk}
	s, err := ctx.API.DmsgSessions()
	if err != nil {
		d.Err = err.Error()
		return d
	}
	d.Clients, d.Count = ui2.DmsgOf(s)
	d.Fetched = time.Now().Format(time.TimeOnly)
	return d
}

// ui2Dmsg serves the tab, or with part set only the polled client list.
func (hv *Hypervisor) ui2Dmsg(part bool) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		d := ui2DmsgState(r.PathValue("pk"), ctx)
		if part {
			hv.render(w, r, ui2.DmsgClients(d))
			return
		}
		hv.render(w, r, ui2.DmsgPage(d))
	})
}

func (hv *Hypervisor) ui2DmsgConnectAll() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		d := ui2.Dmsg{PK: r.PathValue("pk"), Action: hv.ui2T(r, "dmsg-settings.connect-all")}
		res, err := ctx.API.DmsgConnectAll()
		if err != nil {
			d.Err = err.Error()
		}
		d.Result = res
		hv.render(w, r, ui2.DmsgResult(d))
	})
}

// ui2DmsgSessionsCount persists sessions_count and opens sessions up to it,
// as putVisorDmsgSessionsCount does.
func (hv *Hypervisor) ui2DmsgSessionsCount() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		if err := r.ParseForm(); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		raw := r.PostForm.Get("count")
		d := ui2.Dmsg{PK: r.PathValue("pk"), Action: "sessions_count = " + raw}
		n, err := strconv.Atoi(raw)
		switch {
		case err != nil:
			d.Err = err.Error()
		case n < 0:
			d.Err = "count must be >= 0"
		default:
			d.Result, err = ctx.API.SetDmsgSessionsCount(n)
			if err != nil {
				d.Err = err.Error()
			}
		}
		hv.render(w, r, ui2.DmsgResult(d))
	})
}
