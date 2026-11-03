package visor

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/ui2"
	"github.com/skycoin/skywire/pkg/visor/usermanager"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// ui2Client does not follow redirects, so a test sees them.
var ui2Client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func newUI2AuthServer(t *testing.T) (*httptest.Server, cipher.PubKey) {
	t.Helper()
	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{}}, opts: Options{NoCSRF: true}}
	v.conf.LocalPath = filepath.Join(t.TempDir(), "local")
	pk, _ := cipher.GenerateKeyPair()
	store, err := usermanager.NewBoltUserStore(filepath.Join(t.TempDir(), "users.db"))
	require.NoError(t, err)
	// Windows cannot remove an open file, so close it before TempDir cleans up.
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	cc := visorconfig.CookieConfig{HashKey: bytes.Repeat([]byte{1}, 32), BlockKey: bytes.Repeat([]byte{2}, 32)}
	cc.FillDefaults()
	hv := &Hypervisor{
		c:        visorconfig.HypervisorConfig{PK: pk, EnableAuth: true},
		visor:    v,
		mu:       new(sync.RWMutex),
		selfConn: Conn{API: v},
		users:    usermanager.NewUserManager(logging.NewMasterLogger(), store, cc),
	}
	r := httputil.NewRouter()
	hv.ui2Routes(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, pk
}

// TestUI2Login sends a page load without a session to the login page by a
// relative path, makes the first account and logs in with a plain form.
func TestUI2Login(t *testing.T) {
	srv, pk := newUI2AuthServer(t)
	form := "application/x-www-form-urlencoded"

	resp, err := ui2Client.Get(srv.URL + "/ui2/visors/" + pk.Hex() + "/contacts")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	require.Equal(t, "../../../ui2/login", resp.Header.Get("Location"))

	req := ui2Req(t, http.MethodGet, srv.URL+"/ui2/nodes", "", "")
	req.Header.Set("HX-Request", "true")
	resp, err = ui2Client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, "ui2/login", resp.Header.Get("HX-Redirect"))

	code, body := ui2Do(t, ui2Req(t, http.MethodGet, srv.URL+"/ui2/login", "", ""))
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, "Create account")

	mismatch := url.Values{"create": {"1"}, "username": {"admin"}, "password": {"Secret123!"}, "confirm": {"nope"}}
	code, body = ui2Do(t, ui2Req(t, http.MethodPost, srv.URL+"/ui2/login", form, mismatch.Encode()))
	require.Equal(t, http.StatusUnauthorized, code)
	require.Contains(t, body, "do not match")

	create := url.Values{"create": {"1"}, "username": {"admin"}, "password": {"Secret123!"}, "confirm": {"Secret123!"}}
	resp, err = ui2Client.Do(ui2Req(t, http.MethodPost, srv.URL+"/ui2/login", form, create.Encode()))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	require.Equal(t, "./", resp.Header.Get("Location"))
	cookies := resp.Cookies()
	require.NotEmpty(t, cookies, "logging in sets the session cookie")

	req = ui2Req(t, http.MethodGet, srv.URL+"/ui2/visors/"+pk.Hex()+"/contacts", "", "")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	code, body = ui2Do(t, req)
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `hx-post="ui2/logout"`)

	bad := url.Values{"username": {"admin"}, "password": {"wrong"}}
	code, body = ui2Do(t, ui2Req(t, http.MethodPost, srv.URL+"/ui2/login", form, bad.Encode()))
	require.Equal(t, http.StatusUnauthorized, code)
	require.Contains(t, body, "Incorrect password.")
	require.NotContains(t, body, "Create account", "the account exists now")
}

// TestUI2Lang reads the Angular UI's translation files: the cookie first,
// then Accept-Language, and English for a key a language lacks.
func TestUI2Lang(t *testing.T) {
	srv, _, pk := newUI2Server(t, true)
	u := srv.URL + "/ui2/visors/" + pk.Hex() + "/contacts"

	req := ui2Req(t, http.MethodGet, u, "", "")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	_, body := ui2Do(t, req)
	require.Contains(t, body, `<html lang="zh">`)
	require.Contains(t, body, "联系人")

	req = ui2Req(t, http.MethodGet, u, "", "")
	req.Header.Set("Accept-Language", "zh")
	req.AddCookie(&http.Cookie{Name: ui2.LangCookie, Value: "es", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	_, body = ui2Do(t, req)
	require.Contains(t, body, `<html lang="es">`)
	require.Contains(t, body, "No contacts yet.", "a key es.json lacks falls back to English")
}

// TestUI2Static serves the static files gzipped, cached for good when the
// URL names the current content hash.
func TestUI2Static(t *testing.T) {
	srv, _, _ := newUI2Server(t, true)
	v := ui2.AssetVersion("htmx.min.js")
	require.NotEmpty(t, v)

	req := ui2Req(t, http.MethodGet, srv.URL+"/ui2/static/htmx.min.js?v="+v, "", "")
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := (&http.Transport{DisableCompression: true}).RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	require.Contains(t, resp.Header.Get("Cache-Control"), "immutable")
	zr, err := gzip.NewReader(resp.Body)
	require.NoError(t, err)
	b, err := io.ReadAll(zr)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(b), "var htmx="))

	req = ui2Req(t, http.MethodGet, srv.URL+"/ui2/static/ui2.css", "", "")
	req.Header.Set("If-None-Match", `W/"`+ui2.AssetVersion("ui2.css")+`"`)
	resp, err = ui2Client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNotModified, resp.StatusCode)
	require.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
}

// TestUI2NotPorted links a tab the new UI lacks to the classic one, and keeps
// unknown /ui2 paths from reaching the Angular catch-all.
func TestUI2NotPorted(t *testing.T) {
	srv, _, pk := newUI2Server(t, true)
	code, body := ui2Do(t, ui2Req(t, http.MethodGet, srv.URL+"/ui2/visors/"+pk.Hex()+"/vpn", "", ""))
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `href="#/nodes/`+pk.Hex()+`/vpn"`)
	require.Contains(t, body, `class="tab active"`)

	code, _ = ui2Do(t, ui2Req(t, http.MethodGet, srv.URL+"/ui2/visors/"+pk.Hex()+"/nope", "", ""))
	require.Equal(t, http.StatusNotFound, code)
	code, _ = ui2Do(t, ui2Req(t, http.MethodGet, srv.URL+"/ui2/nope", "", ""))
	require.Equal(t, http.StatusNotFound, code)

	resp, err := ui2Client.Get(srv.URL + "/ui2/visors/" + pk.Hex())
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, pk.Hex()+"/info", resp.Header.Get("Location"))
}
