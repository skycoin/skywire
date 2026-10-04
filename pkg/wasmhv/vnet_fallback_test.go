package wasmhv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A /vnet/ request that reached the server gets a page that sends it back
// through the worker, never a cached copy or a bare 404.
func TestServeVNetFallback(t *testing.T) {
	rr := httptest.NewRecorder()
	ServeVNetFallback(rr, httptest.NewRequest(http.MethodGet, "/vnet/8001/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control %q, want no-store", got)
	}
	body := rr.Body.String()
	for _, want := range []string{"location.reload()", "getRegistration", "sessionStorage"} {
		if !strings.Contains(body, want) {
			t.Errorf("fallback page lacks %q", want)
		}
	}
}
