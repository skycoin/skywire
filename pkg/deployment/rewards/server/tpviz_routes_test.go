// Package rewardsserver pkg/deployment/rewards/server/tpviz_routes_test.go c3-app-rewards
package rewardsserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTpvizAssetRoutes checks that the standalone server forwards the assets
// of the default Go WebGL view, the globe and the latency view to tp-viz.
func TestTpvizAssetRoutes(t *testing.T) {
	wd = t.TempDir()
	disableTpVizAPI = false
	t.Cleanup(func() { disableTpVizAPI = true })

	h := buildRouter()
	for _, path := range []string{"/bundle.js", "/tpviz-gl.wasm", "/tpviz-gl-exec.js", "/textures/earth.jpg"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, w.Code, path)
		require.NotZero(t, w.Body.Len(), path)
	}
	for _, path := range []string{"/api/tp-latency", "/api/dmsg/servers/clients"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.NotEqual(t, http.StatusNotFound, w.Code, path)
	}
}
