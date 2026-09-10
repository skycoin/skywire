package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	sdmetrics "github.com/skycoin/skywire/pkg/deployment/sd/metrics"
	"github.com/skycoin/skywire/pkg/deployment/sd/store"
	"github.com/skycoin/skywire/pkg/httpauth"
)

// quietAPI builds an API whose request logging is silenced, so allocation
// counts and benchmark timings measure the router rather than log formatting.
func quietAPI() *API {
	log := logrus.New()
	log.SetOutput(io.Discard)
	log.SetLevel(logrus.PanicLevel)
	return New(log, &store.MockStore{}, nil, false,
		sdmetrics.NewEmpty(), "bench-dmsg-addr", deployment.Prod.GeoIP)
}

// TestRouterRoutesResolve walks every route the router registers and asserts
// it is reachable — i.e. the handler ran and produced its own status rather
// than chi's 404/405. Guards the route table against regressions now that it
// is built once in New instead of per request.
func TestRouterRoutesResolve(t *testing.T) {
	api := newTestAPI(t, &store.MockStore{})

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"health", http.MethodGet, "/health", ""},
		{"get uptimes", http.MethodGet, "/uptimes", ""},
		{"post uptimes", http.MethodPost, "/uptimes", "not json"},
		{"get services", http.MethodGet, "/api/services", ""},
		{"get service by addr", http.MethodGet, "/api/services/someaddr", ""},
		{"post service", http.MethodPost, "/api/services", "not json"},
		{"delete service", http.MethodDelete, "/api/services/someaddr", ""},
		{"deregister", http.MethodDelete, "/api/services/deregister/vpn", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			api.ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
			require.NotEqual(t, http.StatusNotFound, rr.Code, "route did not resolve")
			require.NotEqual(t, http.StatusMethodNotAllowed, rr.Code, "method not registered")
		})
	}

	// An unregistered path must still 404 — the table is not a catch-all.
	rr := httptest.NewRecorder()
	api.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/no-such-route", nil))
	require.Equal(t, http.StatusNotFound, rr.Code)
}

// TestRouterNonceRouteIsConditional pins the conditional registration: the
// nonce endpoint exists only when a nonce store is configured.
func TestRouterNonceRouteIsConditional(t *testing.T) {
	noAuth := newTestAPI(t, &store.MockStore{})
	rr := httptest.NewRecorder()
	noAuth.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/security/nonces/somepk", nil))
	require.Equal(t, http.StatusNotFound, rr.Code)

	nonceDB, err := httpauth.NewNonceStore(context.Background(),
		storeconfig.Config{Type: storeconfig.Memory}, "sd")
	require.NoError(t, err)
	withAuth := New(logging.MustGetLogger("test_sd"), &store.MockStore{}, nonceDB, false,
		sdmetrics.NewEmpty(), "test-dmsg-addr", deployment.Prod.GeoIP)

	rr = httptest.NewRecorder()
	withAuth.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/security/nonces/somepk", nil))
	require.NotEqual(t, http.StatusNotFound, rr.Code)
}

// TestRouterBuiltOnce asserts the router is constructed at New time and that
// serving requests neither replaces it nor builds another one: the handler
// identity is stable across requests, and a request allocates far less than
// building a fresh chi router with six middlewares and ten routes ever could.
func TestRouterBuiltOnce(t *testing.T) {
	api := newTestAPI(t, &store.MockStore{})
	require.NotNil(t, api.Handler, "router must be built in New")

	before := api.Handler
	for i := 0; i < 5; i++ {
		rr := httptest.NewRecorder()
		api.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
		require.Equal(t, http.StatusOK, rr.Code)
		require.Same(t, before, api.Handler, "router was rebuilt while serving")
	}

	// Building the router costs thousands of allocations; serving a
	// health check off a prebuilt one costs tens. The bound is
	// deliberately loose — it only has to fail if construction moves
	// back into the request path.
	quiet := quietAPI()
	allocs := testing.AllocsPerRun(50, func() {
		quiet.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))
	})
	require.Less(t, allocs, float64(400), "per-request allocations suggest the router is rebuilt per request")
}

// BenchmarkServeHTTPHealth measures a simple GET through the router.
func BenchmarkServeHTTPHealth(b *testing.B) {
	api := quietAPI()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		api.ServeHTTP(httptest.NewRecorder(), req)
	}
}
