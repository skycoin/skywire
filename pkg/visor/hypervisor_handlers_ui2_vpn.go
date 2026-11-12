// Package visor pkg/visor/hypervisor_handlers_ui2_vpn.go c3-vis-core
package visor

import (
	"net/http"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The vpn tab: the vpn-client and vpn-server apps. Start, stop, settings
// and flags reuse the apps tab's routes.

func (hv *Hypervisor) ui2VPNRoutes(r *httputil.Router) {
	r.Get("/ui2/visors/{pk}/vpn", hv.ui2VPN(false))
	r.Get("/ui2/visors/{pk}/vpn/body", hv.ui2VPN(true))
}

func (hv *Hypervisor) ui2VPN(body bool) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		v := ui2.VPNOf(ui2LoadApps(ctx, r.PathValue("pk")))
		if body {
			hv.render(w, r, ui2.VPNBody(v))
			return
		}
		hv.render(w, r, ui2.VPNPage(v))
	})
}
