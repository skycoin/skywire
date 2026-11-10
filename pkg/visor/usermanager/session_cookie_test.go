package usermanager

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// A new session's cookie must not ride along on another site's requests.
func TestSessionCookieAttributes(t *testing.T) {
	cfg := visorconfig.CookieConfig{HashKey: make([]byte, 32), BlockKey: make([]byte, 32), Path: "/"}
	cfg.FillDefaults()
	um := NewUserManager(logging.NewMasterLogger(), newMemUserStore(), cfg)

	rec := httptest.NewRecorder()
	if err := um.newSession(rec, Session{User: "admin"}); err != nil {
		t.Fatal(err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	if c := cookies[0]; c.SameSite != http.SameSiteLaxMode || !c.HttpOnly {
		t.Errorf("session cookie SameSite=%v HttpOnly=%v, want Lax and HttpOnly", c.SameSite, c.HttpOnly)
	}
}
