// Package visor pkg/visor/hypervisor_handlers_ui2_vpn.go c3-vis-core
package visor

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The vpn tab: the vpn-client and vpn-server apps. Start, stop, settings
// and flags reuse the apps tab's routes.

func (hv *Hypervisor) ui2VPNRoutes(r *httputil.Router) {
	r.Get("/visors/{pk}/vpn", hv.ui2VPN(false))
	r.Get("/visors/{pk}/vpn/body", hv.ui2VPN(true))
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

// ui2VPNPickRoutes are the server picker of the vpn tab.
func (hv *Hypervisor) ui2VPNPickRoutes(r *httputil.Router) {
	r.Get("/visors/{pk}/vpn/servers", hv.ui2VPNServers())
	r.Post("/visors/{pk}/vpn/use", hv.ui2VPNUse())
}

// ui2VPNServers lists the public servers the service discovery knows, read
// through the same /api/services route the Angular picker calls.
func (hv *Hypervisor) ui2VPNServers() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		app := r.URL.Query().Get("app")
		p := ui2.ServerPick{PK: r.PathValue("pk"), App: app, VPN: !strings.HasPrefix(app, "skysocks"), Q: r.URL.Query().Get("q")}
		if f, err := ctx.API.AppFlags(app); err == nil {
			for _, fl := range f.Flags {
				if fl.Name == "srv" && fl.Set {
					p.Current = fl.Value
				}
			}
		}
		want, accept := "vpn", []string{"vpn"}
		if !p.VPN {
			want, accept = "proxy", []string{"proxy", "skysocks"}
		}
		var all []ui2.ServerRow
		if hv.tpvizServer == nil {
			p.Err = "service discovery view not available on this hypervisor"
		} else {
			r2 := r.Clone(r.Context())
			r2.URL.Path, r2.URL.RawQuery = "/api/services", "type="+want
			cw := &captureWriter{h: http.Header{}, code: http.StatusOK}
			hv.tpvizServer.Handler().ServeHTTP(cw, r2)
			var m map[string]struct {
				PK       string   `json:"pk"`
				Services []string `json:"services"`
				Country  string   `json:"country"`
			}
			switch {
			case cw.code != http.StatusOK:
				p.Err = cw.errText()
			case json.Unmarshal(cw.body.Bytes(), &m) != nil:
				p.Err = "bad service list"
			}
			for _, e := range m {
				for _, s := range e.Services {
					if slices.Contains(accept, s) {
						all = append(all, ui2.ServerRow{PK: e.PK, Country: e.Country})
						break
					}
				}
			}
		}
		hv.render(w, r, ui2.ServerPicker(ui2.FilterServers(p, all)))
	})
}

// ui2VPNUse sets the client's --srv flag, keeping its other flags.
func (hv *Hypervisor) ui2VPNUse() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		app := r.FormValue("app")
		var srv cipher.PubKey
		if err := srv.Set(strings.TrimSpace(r.FormValue("srv"))); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		schema, err := ctx.API.AppFlags(app)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		st, err := ctx.API.App(app)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		values, set := map[string]string{"srv": srv.Hex()}, map[string]bool{"srv": true}
		for _, f := range schema.Flags {
			if f.Set && f.Name != "srv" {
				values[f.Name], set[f.Name] = f.Value, true
			}
		}
		args := ui2.BuildFlagArgs(st.Args, schema.Flags, values, set)
		if err := ui2CallFor(r, r.PathValue("pk"), app, hv.putApp(), map[string]any{"args": ui2.ArgsString(args)}); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		hv.render(w, r, ui2.VPNBody(ui2.VPNOf(ui2LoadApps(ctx, r.PathValue("pk")))), ui2.Toast(hv.ui2T(r, "app-settings.saved"), false))
	})
}
