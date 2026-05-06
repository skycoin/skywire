// Package visor pkg/visor/hypervisor_handlers_router.go
//
// Hypervisor HTTP handlers for the unified visor-wide router-knobs
// resource at GET/PUT /visors/{pk}/router-settings. Backs the
// hypervisor UI's "Router Settings" panel which gives operators
// the same knobs the CLI exposes via 'cli proxy start --xxx'
// (force_local_routes, existing_tp_only, mux_routes, min_hops).
package visor

import (
	"io"
	"net/http"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/usermanager"
)

func (hv *Hypervisor) getRouterSettings() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		s, err := ctx.API.GetRouterSettings()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, s)
	})
}

func (hv *Hypervisor) putRouterSettings() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var s RouterSettings
		if err := httputil.ReadJSON(r, &s); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("putRouterSettings request: %v", err)
			}
			httputil.WriteJSON(w, r, http.StatusBadRequest, usermanager.ErrMalformedRequest)
			return
		}
		if err := ctx.API.SetRouterSettings(s); err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		// Echo back the (post-mutation) state so callers don't have
		// to GET separately to confirm.
		out, err := ctx.API.GetRouterSettings()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, out)
	})
}
