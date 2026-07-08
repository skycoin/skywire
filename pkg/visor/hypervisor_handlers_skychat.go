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

		// SSE / event streams are infinite — the buffered SkychatProxy (io.ReadAll
		// + 30s client timeout) blocks forever on them, which is why the DM live
		// stream showed "Disconnected". Proxy those with flushing instead.
		base := skychatPath
		if i := strings.IndexByte(base, '?'); i >= 0 {
			base = base[:i]
		}
		if base == "sse" || base == "events" || strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") {
			hv.streamSkychatProxy(w, r, skychatPath)
			return
		}

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

// streamSkychatProxy forwards a long-lived (SSE) request to the local skychat
// server and copies the response to the client with per-chunk flushing, so the
// live message stream reaches the hvui in real time instead of being buffered
// until the (never-arriving) end. Canceled when the client disconnects
// (r.Context()). Local visor only, same internal-token bypass as the buffered
// path.
func (hv *Hypervisor) streamSkychatProxy(w http.ResponseWriter, r *http.Request, skychatPath string) {
	addr := skychatLocalAddr(hv.visor)
	if addr == "" {
		http.Error(w, "skychat proxy: addr not configured", http.StatusBadGateway)
		return
	}
	url := "http://" + addr + "/" + strings.TrimPrefix(skychatPath, "/")
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, url, r.Body) //nolint:gosec // visor-controlled localhost addr
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	for k, vv := range r.Header {
		if strings.EqualFold(k, "Host") || strings.EqualFold(k, "Connection") {
			continue
		}
		for _, h := range vv {
			req.Header.Add(k, h)
		}
	}
	req.Header.Set("X-Skychat-Internal-Token", hv.visor.skychatInternalToken())

	// No client Timeout — an SSE stream is long-lived; r.Context() ends it.
	resp, err := (&http.Client{}).Do(req) //nolint:gosec // visor-controlled localhost addr
	if err != nil {
		http.Error(w, "skychat proxy: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close() //nolint:errcheck,gosec
	for k, vv := range resp.Header {
		lk := strings.ToLower(k)
		if lk == "content-length" || lk == "connection" || lk == "transfer-encoding" {
			continue
		}
		for _, h := range vv {
			w.Header().Add(k, h)
		}
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}
	buf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			return
		}
	}
}
