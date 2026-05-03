// Package visor pkg/visor/hypervisor_handlers_skychat.go
//
// Hypervisor HTTP routes for skychat: password get/set/clear and a
// reverse proxy to the local skychat HTTP server. The proxy is
// what backs the hvui's "Chat" tab — the browser talks to the
// hypervisor (already authenticated), the hypervisor adds the
// internal-token header and forwards to localhost:<skychat-port>.
package visor

import (
	"io"
	"net/http"
	"strings"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/usermanager"
)

func (hv *Hypervisor) getSkychatPassword() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		set, err := ctx.API.SkychatPasswordIsSet()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		addr, _ := ctx.API.SkychatLocalAddr() //nolint:errcheck // best effort
		httputil.WriteJSON(w, r, http.StatusOK, struct {
			Set       bool   `json:"set"`
			LocalAddr string `json:"local_addr,omitempty"`
		}{Set: set, LocalAddr: addr})
	})
}

func (hv *Hypervisor) putSkychatPassword() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var rb struct {
			OldPassword string `json:"old_password"`
			NewPassword string `json:"new_password"`
		}
		if err := httputil.ReadJSON(r, &rb); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("putSkychatPassword: %v", err)
			}
			httputil.WriteJSON(w, r, http.StatusBadRequest, usermanager.ErrMalformedRequest)
			return
		}
		if err := ctx.API.SetSkychatPassword(rb.OldPassword, rb.NewPassword); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, true)
	})
}

func (hv *Hypervisor) deleteSkychatPassword() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		// Accept the old password from either a JSON body or a
		// query string — Angular's HttpClient DELETE doesn't
		// naturally carry a body, so the hvui sends ?old_password=.
		var rb struct {
			OldPassword string `json:"old_password"`
		}
		_ = httputil.ReadJSON(r, &rb) //nolint:errcheck
		if rb.OldPassword == "" {
			rb.OldPassword = r.URL.Query().Get("old_password")
		}
		if err := ctx.API.ClearSkychatPassword(rb.OldPassword); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, true)
	})
}

// skychatProxyHandler forwards every request under
// /api/visors/<pk>/skychat/proxy/* to the local skychat HTTP server
// at the visor's configured skychat addr. The hypervisor is already
// authenticated; the proxy adds the internal-token header so any
// password gate skychat has set on the standalone :8001 surface is
// bypassed for in-process hvui calls.
//
// For now this only supports the local visor — proxying skychat
// for remote visors would need streaming RPC which net/rpc doesn't
// naturally do. Callers targeting a remote visor get 501.
func (hv *Hypervisor) skychatProxyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.visor == nil {
			http.Error(w, "skychat proxy: no local visor", http.StatusNotImplemented)
			return
		}
		path := r.URL.Path
		idx := strings.Index(path, "/skychat/proxy/")
		if idx < 0 {
			http.Error(w, "bad skychat proxy URL", http.StatusBadRequest)
			return
		}
		skychatPath := strings.TrimPrefix(path[idx+len("/skychat/proxy"):], "/")

		status, hdr, body, err := hv.visor.SkychatProxy(r.Method, skychatPath, r.URL.RawQuery, r.Header, r.Body)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadGateway, err)
			return
		}
		for k, vv := range hdr {
			lk := strings.ToLower(k)
			if lk == "content-length" || lk == "connection" || lk == "transfer-encoding" {
				continue
			}
			for _, h := range vv {
				w.Header().Add(k, h)
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write(body) //nolint:errcheck,gosec
	}
}
