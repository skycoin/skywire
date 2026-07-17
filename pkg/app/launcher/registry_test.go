// Package launcher pkg/app/launcher/registry_test.go
package launcher

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHTTPHandlerRegistry covers the in-process HTTP-handler registry that
// backs portless-internal apps (skychat): register → get → serve in-process →
// clear. This is the seam the visor's control surface uses instead of a
// loopback dial when an app runs with no TCP port of its own.
func TestHTTPHandlerRegistry(t *testing.T) {
	const name = "test-portless-app"

	if h := GetHTTPHandler(name); h != nil {
		t.Fatalf("expected no handler before registration, got %v", h)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
	})
	RegisterHTTPHandler(name, mux)

	h := GetHTTPHandler(name)
	if h == nil {
		t.Fatal("expected handler after registration, got nil")
	}

	// Serve in-process, the way Visor.SkychatProxy does.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	h.ServeHTTP(rec, req)
	res := rec.Result()
	body, _ := io.ReadAll(res.Body) //nolint:errcheck // test read; error not material
	_ = res.Body.Close()            //nolint:errcheck
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got, want := string(body), `{"ok":true}`; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}

	// Re-registration overwrites (an app restart re-publishes).
	RegisterHTTPHandler(name, http.NewServeMux())
	if GetHTTPHandler(name) == nil {
		t.Fatal("expected handler after re-registration")
	}

	// nil clears (shutdown).
	RegisterHTTPHandler(name, nil)
	if h := GetHTTPHandler(name); h != nil {
		t.Fatalf("expected handler cleared after nil registration, got %v", h)
	}
}
