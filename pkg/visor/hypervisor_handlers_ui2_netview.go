// Package visor pkg/visor/hypervisor_handlers_ui2_netview.go c3-vis-core
package visor

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// The network view: the visor's cached aggregation of the service
// discovery, transport discovery and uptime tracker.

func (hv *Hypervisor) ui2NetViewRoutes(r *httputil.Router) {
	r.Get("/ui2/network", hv.ui2NetView(false))
	r.Get("/ui2/network/body", hv.ui2NetView(true))
}

func (hv *Hypervisor) ui2NetView(body bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(90 * time.Second)) //nolint:errcheck
		var resp *visorapi.NetworkViewResponse
		var err error
		switch {
		case hv.visor == nil:
			err = errVisorTimeout
		case r.FormValue("refresh") != "":
			resp, err = hv.visor.NetworkViewRefresh()
		default:
			resp, err = hv.visor.NetworkView()
		}
		minT, _ := strconv.Atoi(r.FormValue("min")) //nolint:errcheck
		all := r.FormValue("filtered") != "" && r.FormValue("online") == ""
		var v ui2.NetView
		if resp != nil {
			v = ui2.NetViewOf(resp.Entries, ui2.ParseListQuery(r.FormValue), strings.TrimSpace(r.FormValue("country")), strings.TrimSpace(r.FormValue("version")), minT, all)
			v.Fetched = resp.FetchedAt.Local().Format(time.TimeOnly)
		}
		if err != nil {
			v.Err = err.Error()
		}
		if body {
			hv.render(w, r, ui2.NetViewBody(v))
			return
		}
		hv.render(w, r, ui2.NetViewPage(v))
	}
}
