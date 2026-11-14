// Package visor pkg/visor/hypervisor_handlers_ui2_webproxy.go c3-vis-core
package visor

import (
	"net/http"
	"strings"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The web proxy tab: the embedded .dmsg and .skynet resolving proxies.
// Writes go through the JSON proxy handlers the Angular tab calls.

func (hv *Hypervisor) ui2WebProxyRoutes(r *httputil.Router) {
	r.Get("/visors/{pk}/web-proxy", hv.ui2WebProxy(""))
	r.Get("/visors/{pk}/web-proxy/body", hv.ui2WebProxy("body"))
	r.Put("/visors/{pk}/web-proxy/enabled", hv.ui2WebProxy("enabled"))
	r.Put("/visors/{pk}/web-proxy/upstream", hv.ui2WebProxy("upstream"))
}

func (hv *Hypervisor) ui2WebProxy(op string) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var done string
		switch op {
		case "enabled":
			on := r.FormValue("on") != ""
			// The Angular tab switches both resolvers together.
			for _, kind := range []string{"skynet", "dmsg"} {
				if !ui2CallAPI(w, r, hv.postProxyEnabled(), map[string]any{"kind": kind, "enable": on}) {
					return
				}
			}
			done = hv.ui2T(r, "apps.operation-completed")
		case "upstream":
			if !ui2CallAPI(w, r, hv.postProxyUpstream(), map[string]string{"kind": "skynet", "addr": strings.TrimSpace(r.FormValue("addr"))}) {
				return
			}
			done = hv.ui2T(r, "apps.operation-completed")
		}
		p := ui2.WebProxy{PK: r.PathValue("pk")}
		if st, err := ctx.API.EmbeddedProxies(); err != nil {
			p.Err = err.Error()
		} else {
			p.Status = st
		}
		switch {
		case op == "":
			hv.render(w, r, ui2.WebProxyPage(p))
		case done != "":
			hv.render(w, r, ui2.WebProxyBody(p), ui2.Toast(done, false))
		default:
			hv.render(w, r, ui2.WebProxyBody(p))
		}
	})
}
