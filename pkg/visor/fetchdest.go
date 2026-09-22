// Package visor pkg/visor/fetchdest.go c3-vis-core
package visor

import "net/http"

// fetchDest reports what the browser said this request was FOR — "document"
// for a top-level navigation, "iframe" for a framed one, "script", "image" and
// so on for subresources. Empty when nothing said.
//
// Sec-Fetch-Dest is the browser's own header and is authoritative whenever the
// request reached us over the network. It does NOT survive a service worker:
// it is a forbidden header name, so a worker rebuilding the request can
// neither read it nor pass it on, and every request served over bottle's vnet
// wire arrives without it. The worker forwards request.destination — the same
// fact, readable — as X-Vnet-Fetch-Dest instead, so accept either.
//
// Lives in its own file, unconstrained by build tags, because both users need
// it and they do not share one: the hypervisor UI handler is !mobile, the wasm
// PWA's wallet bounce is built everywhere.
func fetchDest(r *http.Request) string {
	if d := r.Header.Get("Sec-Fetch-Dest"); d != "" {
		return d
	}
	return r.Header.Get("X-Vnet-Fetch-Dest")
}
