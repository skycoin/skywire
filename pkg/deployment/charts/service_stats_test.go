// Package charts pkg/deployment/charts/service_stats_test.go c4-net-discovery
package charts

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/httputil"
)

// Requests are counted under the route that matched, not the path asked for,
// with their status class and the bytes sent, and a sample carries the
// process figures and response time percentiles.
func TestServiceStatsCountsByRoute(t *testing.T) {
	s := NewServiceStats("test")
	r := httputil.NewRouter()
	r.Get("/entries/{pk}", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hello")) }) //nolint:errcheck
	r.Get("/missing", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	h := s.Handler(r)
	for _, p := range []string{"/entries/a", "/entries/b", "/missing", "/nowhere/x"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}

	v := map[string]float64{}
	s.Collect(v)
	require.Equal(t, 2.0, v[prefixReq+"GET /entries/{pk}"])
	require.Equal(t, 1.0, v[prefixReq+"GET /missing"])
	require.Equal(t, 1.0, v[prefixReq+"GET /nowhere"], "an unrouted path counts by its first segment")
	require.Equal(t, 2.0, v[prefixCode+"2xx"])
	require.Equal(t, 2.0, v[prefixCode+"4xx"])
	require.Equal(t, 10.0, v[prefixHTTPOut+"GET /entries/{pk}"])
	require.Contains(t, v, keyHTTPP50)
	require.Contains(t, v, keyGoroutines)
	require.Contains(t, v, keyHeap)

	// The next interval starts from zero. CPU is a rate, and a coarse clock
	// (Windows) reads back to back samples as no time apart.
	time.Sleep(50 * time.Millisecond)
	v = map[string]float64{}
	s.Collect(v)
	require.NotContains(t, v, prefixReq+"GET /missing")
	require.Contains(t, v, keyCPU, "CPU is a rate, known from the second sample on")
}
