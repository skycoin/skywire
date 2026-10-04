package logserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func TestRoutes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, skyenv.RewardFile), []byte("reward-address"), 0o600); err != nil {
		t.Fatal(err)
	}
	allowed, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	api := New(logging.MustGetLogger("logserver-test"), dir, "", []cipher.PubKey{allowed}, &visorconfig.Survey{}, true)

	get := func(method, path string, pk cipher.PubKey) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = pk.String() + ":49153"
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, req)
		return rec
	}

	cases := []struct {
		method, path string
		pk           cipher.PubKey
		code         int
		body         string
	}{
		{"GET", "/health", other, http.StatusOK, `"service_name":"visor"`},
		{"GET", "/services", other, http.StatusOK, "[]"},
		{"GET", "/feeds", other, http.StatusOK, "[]"},
		{"GET", "/", other, http.StatusOK, "/health"},
		{"GET", "/node-info", other, http.StatusUnauthorized, ""},
		{"GET", "/node-info", allowed, http.StatusOK, "{"},
		{"GET", "/node-info/checksum", allowed, http.StatusOK, `"sha256"`},
		{"GET", "/" + skyenv.RewardFile, allowed, http.StatusOK, "reward-address"},
		{"GET", "/" + skyenv.RewardFile, other, http.StatusUnauthorized, ""},
		{"GET", "/debug/pprof/", allowed, http.StatusOK, "goroutine"},
		{"GET", "/debug/pprof/goroutine?debug=1", allowed, http.StatusOK, "goroutine profile"},
		{"GET", "/debug/pprof/goroutine", other, http.StatusUnauthorized, ""},
		{"GET", "/debug/pprof", allowed, http.StatusTemporaryRedirect, "/debug/pprof/"},
		{"GET", "/stats/transports", allowed, http.StatusServiceUnavailable, "stats store not available"},
		{"GET", "/uptime/now", allowed, http.StatusServiceUnavailable, ""},
		{"GET", "/pty", allowed, http.StatusNotFound, ""},
		{"GET", "/pty/ws", allowed, http.StatusNotFound, ""},
		{"GET", "/skywire.log", allowed, http.StatusNotFound, "not found"},
		{"GET", "/no-such-page", other, http.StatusNotFound, "404 not found"},
		{"POST", "/health", other, http.StatusNotFound, "404 not found"},
	}
	for _, tc := range cases {
		rec := get(tc.method, tc.path, tc.pk)
		if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.body) {
			t.Errorf("%s %s: got %d %q, want %d containing %q", tc.method, tc.path, rec.Code, rec.Body.String(), tc.code, tc.body)
		}
	}

	api.SetWebsiteHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("website")) //nolint:errcheck
	}))
	for _, path := range []string{"/", "/no-such-page"} {
		if rec := get("GET", path, other); rec.Code != http.StatusOK || rec.Body.String() != "website" {
			t.Errorf("GET %s with a website: got %d %q", path, rec.Code, rec.Body.String())
		}
	}
	if rec := get("GET", "/health", other); !strings.Contains(rec.Body.String(), `"service_name":"visor"`) {
		t.Errorf("GET /health with a website: got %q", rec.Body.String())
	}
}
