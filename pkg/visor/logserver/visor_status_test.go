package logserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// The visor's own status page answers the visor itself and the survey
// whitelist, and nobody else, even with an empty whitelist.
func TestVisorStatusPrivate(t *testing.T) {
	self, _ := cipher.GenerateKeyPair()
	allowed, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	hv, _ := cipher.GenerateKeyPair()
	page := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("visor charts")) }) //nolint:errcheck,gosec

	for _, wl := range [][]cipher.PubKey{nil, {allowed}} {
		api := New(logging.MustGetLogger("logserver-test"), t.TempDir(), "", wl, &visorconfig.Survey{}, true)
		api.SetIdentity(self.Hex(), "")
		api.SetVisorStatusPage(page, func() []cipher.PubKey { return []cipher.PubKey{hv} })
		get := func(path string, pk cipher.PubKey) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.RemoteAddr = pk.String() + ":80"
			rec := httptest.NewRecorder()
			api.ServeHTTP(rec, req)
			return rec
		}
		if rec := get("/status/visor", self); rec.Code != http.StatusOK || rec.Body.String() != "visor charts" {
			t.Fatalf("whitelist %v: own request got %d %q", wl, rec.Code, rec.Body.String())
		}
		if rec := get("/status/visor", hv); rec.Code != http.StatusOK {
			t.Fatalf("whitelist %v: the visor's hypervisor got %d", wl, rec.Code)
		}
		if rec := get("/status/visor", other); rec.Code != http.StatusUnauthorized {
			t.Fatalf("whitelist %v: stranger got %d", wl, rec.Code)
		}
		if strings.Contains(get("/", other).Body.String(), "/status/visor") {
			t.Fatalf("whitelist %v: landing page links the stranger to /status/visor", wl)
		}
		if wl != nil {
			if rec := get("/status/visor", allowed); rec.Code != http.StatusOK {
				t.Fatalf("whitelisted key got %d", rec.Code)
			}
			if !strings.Contains(get("/", allowed).Body.String(), "/status/visor") {
				t.Fatal("landing page does not link the whitelisted key to /status/visor")
			}
		}
	}
}
