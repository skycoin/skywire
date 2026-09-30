package visor

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// The page's cookies become the Cookie header, less any the jar will send
// itself, and the carrier header is removed.
func TestApplyPageCookies(t *testing.T) {
	u, err := url.Parse("https://example.org/x")
	require.NoError(t, err)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "jar", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}})
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	require.NoError(t, err)
	req.Header.Set(browsePageCookieHeader, "session=page; theme=dark")
	applyPageCookies(req, jar)
	require.Empty(t, req.Header.Get(browsePageCookieHeader))
	require.Equal(t, "theme=dark", req.Header.Get("Cookie"))
}
