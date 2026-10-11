package logserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// /status answers 404 until the visor sets a page, then serves it, and the
// landing page links it only then.
func TestStatusPage(t *testing.T) {
	api := New(logging.MustGetLogger("test"), t.TempDir(), "", nil, &visorconfig.Survey{}, false)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		api.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	if w := get("/status"); w.Code != http.StatusNotFound {
		t.Fatalf("unset /status: %d, want 404", w.Code)
	}
	if strings.Contains(get("/").Body.String(), `href="/status"`) {
		t.Error("landing page links /status before it is set")
	}
	api.SetStatusPage(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("board")) })) //nolint:errcheck
	if w := get("/status"); w.Code != http.StatusOK || w.Body.String() != "board" {
		t.Fatalf("/status: %d %q", w.Code, w.Body.String())
	}
	if !strings.Contains(get("/").Body.String(), `href="/status"`) {
		t.Error("landing page does not link /status")
	}
}
