// Package logserver pkg/visor/logserver/vhost_test.go
package logserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func site(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, name+" "+r.URL.Path) //nolint:errcheck,gosec // a test fixture
	})
}

func TestVhosts(t *testing.T) {
	visorPK, _ := cipher.GenerateKeyPair()
	friend, _ := cipher.GenerateKeyPair()
	stranger, _ := cipher.GenerateKeyPair()
	api := New(logging.MustGetLogger("test"), t.TempDir(), "", nil, &visorconfig.Survey{}, false)
	api.SetIdentity(visorPK.Hex(), "")
	api.SetVhosts([]Vhost{
		{Name: "status", Names: []string{"status", "status.skywire.dev"}, Handler: site("status")},
		{Name: "private", Names: []string{"private"}, Handler: site("private"), Whitelist: []cipher.PubKey{friend}},
	})
	get := func(host, path string, from cipher.PubKey) (int, string) {
		req := httptest.NewRequest(http.MethodGet, "http://x"+path, nil)
		req.Host = host
		req.RemoteAddr = from.String() + ":80"
		rec := httptest.NewRecorder()
		api.Handler.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	label := visorPK.DNSLabel()

	for _, host := range []string{
		"status",                            // as the resolving proxy rewrites it
		"status.skywire.dev",                // an alias
		"STATUS.Skywire.Dev:80",             // case and port do not matter
		"status." + label + ".skynet",       // not rewritten
		"status." + visorPK.Hex() + ".dmsg", // the hex form, over dmsg
		"status.skywire.dev." + label + ".skynet",
	} {
		code, body := get(host, "/", stranger)
		require.Equal(t, http.StatusOK, code, host)
		require.Equal(t, "status /", body, host)
	}
	// A site gets every path, the visor's own included.
	_, body := get("status", "/health", stranger)
	require.Equal(t, "status /health", body)

	// The bare name is still the visor, and its landing page links the sites
	// the caller may reach.
	code, body := get(label+".skynet", "/health", stranger)
	require.Equal(t, http.StatusOK, code)
	require.NotContains(t, body, "status /health")
	_, body = get(label+".skynet", "/", stranger)
	require.Contains(t, body, "http://status."+label+".skynet/")
	require.NotContains(t, body, "http://private.")
	_, body = get(label+".skynet", "/", friend)
	require.Contains(t, body, "http://private."+label+".skynet/")

	code, _ = get("private", "/", stranger)
	require.Equal(t, http.StatusForbidden, code)
	code, body = get("private", "/", friend)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "private /", body)

	// An unknown name falls through to the visor as before.
	_, body = get("nosuch."+label+".skynet", "/", stranger)
	require.False(t, strings.HasPrefix(body, "status"))

	api.SetVhosts(nil)
	_, body = get("status", "/", stranger)
	require.NotEqual(t, "status /", body, "removed sites are no longer served")
}

func TestVhostName(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	for host, want := range map[string]string{
		"status":                           "status",
		"status.skywire.dev.":              "status.skywire.dev",
		pk.DNSLabel() + ".skynet":          "",
		pk.Hex() + ".dmsg:80":              "",
		"a.b." + pk.DNSLabel() + ".skynet": "a.b",
		"not-a-pk.skynet":                  "",
	} {
		require.Equal(t, want, vhostName(host), host)
	}
}

// The vhost * takes every name the others do not, and gets the short name as
// its Host; the bare name stays the visor, whose own website is separate.
func TestVhostCatchAll(t *testing.T) {
	visorPK, _ := cipher.GenerateKeyPair()
	api := New(logging.MustGetLogger("test"), t.TempDir(), "", nil, &visorconfig.Survey{}, false)
	api.SetIdentity(visorPK.Hex(), "")
	echo := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, name+" "+r.Host+" "+r.URL.Path) //nolint:errcheck,gosec // a test fixture
		})
	}
	api.SetVhosts([]Vhost{
		{Name: "status", Names: []string{"status"}, Handler: echo("status")},
		{Name: AnyVhost, Names: []string{AnyVhost}, Handler: echo("caddy")},
	})
	api.SetWebsiteHandler(echo("website"))
	get := func(host, path string) string {
		req := httptest.NewRequest(http.MethodGet, "http://x"+path, nil)
		req.Host = host
		rec := httptest.NewRecorder()
		api.Handler.ServeHTTP(rec, req)
		return rec.Body.String()
	}
	label := visorPK.DNSLabel()

	require.Equal(t, "status status /a", get("status."+label+".skynet", "/a"))
	require.Equal(t, "caddy skywire.dev /", get("skywire.dev", "/"))
	require.Equal(t, "caddy skywire.dev /health", get("skywire.dev."+label+".skynet:80", "/health"))
	require.Equal(t, "caddy blog.example.org /x", get("blog.example.org."+visorPK.Hex()+".dmsg", "/x"))

	for _, own := range []string{label + ".skynet", visorPK.Hex() + ".dmsg", visorPK.Hex() + ":80", label, "127.0.0.1:80", "[::1]:80", "localhost", ""} {
		require.Equal(t, "website "+own+" /", get(own, "/"), own)
		require.NotContains(t, get(own, "/health"), "caddy", own)
	}
}

// The visor's own pages stay reachable when a site takes its bare name: at
// /.skywire/, under a name set to show them, and through OwnPages on a port.
func TestVisorPagesAndSites(t *testing.T) {
	visorPK, _ := cipher.GenerateKeyPair()
	friend, _ := cipher.GenerateKeyPair()
	api := New(logging.MustGetLogger("test"), t.TempDir(), "", nil, &visorconfig.Survey{}, false)
	api.SetIdentity(visorPK.Hex(), "")
	api.SetVhosts([]Vhost{
		{Name: "status", Names: []string{"status"}, Handler: site("status")},
		{Name: "private", Names: []string{"private"}, Handler: site("private"), Whitelist: []cipher.PubKey{friend}},
		{Name: AnyVhost, Names: []string{AnyVhost}, Handler: site("caddy"), Sites: func() []string { return []string{"skycoin.com", "status"} }},
		{Name: "landing", Names: []string{"landing"}, VisorPages: true},
	})
	api.SetWebsiteHandler(site("website"))
	label := visorPK.DNSLabel()
	get := func(h http.Handler, host, path string) string {
		req := httptest.NewRequest(http.MethodGet, "http://x"+path, nil)
		req.Host = host
		req.RemoteAddr = visorPK.String() + ":80"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Body.String()
	}

	require.Equal(t, "website /", get(api.Handler, label+".skynet", "/"))
	for _, page := range []string{
		get(api.Handler, label+".skynet", "/.skywire/"),
		get(api.Handler, "landing."+label+".skynet", "/"),
		get(api.OwnPages(), label+".skynet:8000", "/"),
	} {
		require.Contains(t, page, `href="/health"`)
		require.Contains(t, page, "http://skycoin.com."+label+".skynet/")
		require.Contains(t, page, "http://status."+label+".skynet/")
		require.NotContains(t, page, "http://landing.")
		require.NotContains(t, page, "http://private.")
	}
	require.Contains(t, get(api.Handler, "landing."+label+".skynet", "/health"), "{")

	var list SiteList
	require.NoError(t, json.Unmarshal([]byte(get(api.Handler, label+".skynet", "/.skywire/sites")), &list))
	require.Equal(t, SiteList{PK: visorPK.Hex(), Sites: []string{"skycoin.com", "status"}}, list)
}

// home.skynet shows what only the visor's user should see to the visor itself.
func TestHomePageOwnUser(t *testing.T) {
	visorPK, _ := cipher.GenerateKeyPair()
	stranger, _ := cipher.GenerateKeyPair()
	api := New(logging.MustGetLogger("test"), t.TempDir(), "", nil, &visorconfig.Survey{}, false)
	api.SetIdentity(visorPK.Hex(), "")
	api.SetHomePage(func(suffix string, ownUser bool) []byte {
		if ownUser {
			return []byte("private" + suffix)
		}
		return []byte("public" + suffix)
	})
	get := func(from cipher.PubKey) string {
		req := httptest.NewRequest(http.MethodGet, "http://home.skynet/", nil)
		req.RemoteAddr = from.String() + ":80"
		rec := httptest.NewRecorder()
		api.Handler.ServeHTTP(rec, req)
		return rec.Body.String()
	}
	require.Equal(t, "private.skynet", get(visorPK))
	require.Equal(t, "public.skynet", get(stranger))
}

type portWhitelist []cipher.PubKey

func (portWhitelist) LandingPageEntries() []ForwardedPortEntry { return nil }
func (p portWhitelist) PortWhitelist(int) []cipher.PubKey      { return p }

// A site on port 80 with a whitelist is closed to other keys at every path.
func TestWebsiteWhitelist(t *testing.T) {
	visorPK, _ := cipher.GenerateKeyPair()
	friend, _ := cipher.GenerateKeyPair()
	stranger, _ := cipher.GenerateKeyPair()
	api := New(logging.MustGetLogger("test"), t.TempDir(), "", nil, &visorconfig.Survey{}, false)
	api.SetIdentity(visorPK.Hex(), "")
	api.SetForwardedPortLister(portWhitelist{friend})
	api.SetWebsiteHandler(site("site"))
	get := func(path string, from cipher.PubKey) int {
		req := httptest.NewRequest(http.MethodGet, "http://"+visorPK.DNSLabel()+".skynet"+path, nil)
		req.RemoteAddr = from.String() + ":80"
		rec := httptest.NewRecorder()
		api.Handler.ServeHTTP(rec, req)
		return rec.Code
	}
	for _, path := range []string{"/", "/page"} {
		require.Equal(t, http.StatusOK, get(path, friend), path)
		require.Equal(t, http.StatusForbidden, get(path, stranger), path)
	}
}
