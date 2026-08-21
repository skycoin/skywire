//go:build !mobile

package visor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGetRouteViz checks the embedded route-visualizer page is served with the
// right content-type and carries its data-endpoint + telemetry field wiring, so
// a rename of the /route-mux seam or the MuxLegInfo JSON tags is caught here.
func TestGetRouteViz(t *testing.T) {
	hv := &Hypervisor{}
	w := httptest.NewRecorder()
	hv.getRouteViz()(w, httptest.NewRequest(http.MethodGet, "/route-viz", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type=%q, want text/html", ct)
	}
	body := w.Body.String()
	for _, want := range []string{
		"/api/visors/",            // visor list fetch
		"/route-mux?app=",         // the live per-leg data endpoint
		"remote_pk", "latency_ms", // MuxLegInfo JSON contract the page renders
		"standby", "retransmits", // gate-state + loss signal
	} {
		if !strings.Contains(body, want) {
			t.Errorf("route-viz page missing %q", want)
		}
	}
}
