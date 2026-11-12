// Package visor pkg/visor/hypervisor_handlers_ui2_logs.go c3-vis-core
package visor

import (
	"net/http"
	"strconv"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The logs tab of the server rendered UI. It polls RuntimeLogsSince with a
// cursor every two seconds, as the Angular tab does, and appends what is new.

func (hv *Hypervisor) ui2LogsRoutes(r *httputil.Router) {
	r.Get("/visors/{pk}/logs", hv.ui2Logs(false))
	r.Get("/visors/{pk}/logs/list", hv.ui2Logs(true))
	r.Get("/visors/{pk}/logs/more", hv.ui2LogsMore())
}

// ui2Logs serves the page, or with part set the list a filter change swaps.
func (hv *Hypervisor) ui2Logs(part bool) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		l := ui2.Logs{PK: r.PathValue("pk")}
		f, err := ui2.ParseLogFilter(r.URL.Query())
		l.Filter = f
		if err != nil {
			l.Err = err.Error()
		}
		delta, err := ctx.API.RuntimeLogsSince(0)
		if err != nil {
			l.Err = err.Error()
		} else {
			l.Entries, l.More = ui2.FilterLogs(delta.Entries, f)
			l.Cursor, l.Total = delta.Latest, delta.Latest
		}
		if part {
			hv.render(w, r, ui2.LogBody(l))
			return
		}
		hv.render(w, r, ui2.LogsPage(l))
	})
}

// ui2LogsMore is one poll: the entries after ?since, filtered.
func (hv *Hypervisor) ui2LogsMore() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		q := r.URL.Query()
		since, err := strconv.ParseInt(q.Get("since"), 10, 64)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		dropped, _ := strconv.ParseInt(q.Get("dropped"), 10, 64) //nolint:errcheck
		f, _ := ui2.ParseLogFilter(q)                            //nolint:errcheck
		l := ui2.Logs{PK: r.PathValue("pk"), Filter: f, Cursor: since, Dropped: dropped}
		delta, err := ctx.API.RuntimeLogsSince(since)
		if err != nil {
			// Keep polling: the next poll may reach the visor again.
			hv.render(w, r, ui2.LogMore(l))
			return
		}
		l.Entries, _ = ui2.FilterLogs(delta.Entries, f)
		l.Cursor = delta.Latest
		l.Dropped += delta.Dropped
		hv.render(w, r, ui2.LogMore(l))
	})
}
