// Package visor pkg/visor/hypervisor_handlers_skyenv.go c3-vis-core
package visor

import (
	"io"
	"net/http"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/usermanager"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// getSkyenv serves the visor's /etc/skywire.conf settings. See api_skyenv.go.
func (hv *Hypervisor) getSkyenv() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		st, err := ctx.API.Skyenv()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, st)
	})
}

// putSkyenv edits /etc/skywire.conf as `skywire autoconfig --<flag>` would.
// Every failure is the edit not being made, and its message says why, so
// they are all reported as 400.
func (hv *Hypervisor) putSkyenv() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var req visorapi.SkyenvEdits
		if err := httputil.ReadJSON(r, &req); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("putSkyenv request: %v", err)
			}
			httputil.WriteJSON(w, r, http.StatusBadRequest, usermanager.ErrMalformedRequest)
			return
		}
		st, err := ctx.API.SetSkyenv(req)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, st)
	})
}
