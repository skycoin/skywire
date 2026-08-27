// Package visor pkg/visor/api_services_probe_test.go c3-vis-core
package visor

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestDoHealthProbe_BoundsHangingEndpoint pins the fix for the ~10s ServiceHealth
// stall: a single unresponsive /health endpoint must NOT block the probe for the
// underlying client's full timeout. doHealthProbe caps each request at
// healthProbeTimeout, so a dead endpoint returns DOWN promptly instead of
// stalling ServiceHealth (and the `visor state` snapshot that folds it in).
func TestDoHealthProbe_BoundsHangingEndpoint(t *testing.T) {
	// A server that never responds within the probe budget: it blocks until the
	// request context is canceled — which is exactly what doHealthProbe's timeout
	// does to the underlying connection. That both exercises the bound and lets
	// httptest tear down promptly once the probe gives up.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	start := time.Now()
	entry := doHealthProbe(srv.Client(), "Hanging", srv.URL, "http")
	elapsed := time.Since(start)

	// Must return well before the server would have (10s past the budget),
	// bounded by healthProbeTimeout plus a little scheduling slack.
	if elapsed > healthProbeTimeout+2*time.Second {
		t.Fatalf("probe took %v; expected it bounded near healthProbeTimeout (%v)", elapsed, healthProbeTimeout)
	}
	if entry.Status != "DOWN" {
		t.Fatalf("hanging endpoint status = %q; want DOWN", entry.Status)
	}
}

// TestDoHealthProbe_HealthyIsUnaffected confirms the bound does not penalize a
// normally-responding service: it returns OK quickly with the version parsed.
func TestDoHealthProbe_HealthyIsUnaffected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"build_info":{"version":"v1.2.3"}}`)); err != nil {
			t.Errorf("write health body: %v", err)
		}
	}))
	defer srv.Close()

	start := time.Now()
	entry := doHealthProbe(srv.Client(), "Healthy", srv.URL, "http")
	elapsed := time.Since(start)

	if elapsed > healthProbeTimeout {
		t.Fatalf("healthy probe took %v; should be near-instant", elapsed)
	}
	if entry.Status != "OK" {
		t.Fatalf("healthy status = %q; want OK", entry.Status)
	}
	if entry.Version != "v1.2.3" {
		t.Fatalf("version = %q; want v1.2.3", entry.Version)
	}
}
