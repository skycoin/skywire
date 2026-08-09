//go:build mobile

// Package visor pkg/visor/hypervisor_handlers_browse_mobile.go c3-vis-core
// Mobile build: the in-browser mesh browser and the embedded manager UI are
// desktop features. The /api/* surface is untouched; "/" serves a one-line
// identification page so a health-check in a browser shows the build is alive.
package visor

import "net/http"

const mobileIndexHTML = `<!doctype html><html><head><title>skywire-mobile</title></head>` +
	`<body>skywire-mobile core &mdash; API-only build. The manager UI is not included; ` +
	`the visor&#39;s authenticated local API is served under /api/.</body></html>`

// postBrowseFetch → 404: the in-UI mesh browser is not part of the mobile build.
func (hv *Hypervisor) postBrowseFetch() http.HandlerFunc {
	return browseNotAvailable
}

// postBrowseClearnet → 404: the in-UI mesh browser is not part of the mobile build.
func (hv *Hypervisor) postBrowseClearnet() http.HandlerFunc {
	return browseNotAvailable
}

func browseNotAvailable(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "browse is not available in this build", http.StatusNotFound)
}

// uiHandler replaces the embedded manager UI: "/" identifies the build; every
// other non-API path 404s (the API routes are matched before this catch-all).
func (hv *Hypervisor) uiHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write([]byte(mobileIndexHTML)) //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	})
}

// getUIVersion answers an empty fingerprint: there is no embedded UI to version.
func (hv *Hypervisor) getUIVersion() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "no-store")
	}
}
