// Package visor pkg/visor/routeviz_embed.go c3-vis-core
package visor

import (
	_ "embed"
	"net/http"
)

// routeVizHTML is the self-contained route-visualizer page (vanilla JS, no
// build step). Served at /route-viz; it fetches /api/visors and
// /api/visors/{pk}/route-mux same-origin to live-render an app's active
// route group(s) per leg. See docs/design/route-visualizer.md.
//
//go:embed routeviz/routeviz.html
var routeVizHTML []byte

// getRouteViz serves the embedded route-visualizer page. Static, no auth of
// its own — the data it fetches (/api/...) carries the hypervisor's auth.
func (hv *Hypervisor) getRouteViz() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if _, err := w.Write(routeVizHTML); err != nil {
			hv.log(r).WithError(err).Debug("route-viz page write failed")
		}
	}
}
