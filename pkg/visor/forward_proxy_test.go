package visor

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// newProxyHandler builds a forwardProxyHandler with a default (direct) egress
// client for tests.
func newProxyHandler(t *testing.T, direct bool) *forwardProxyHandler {
	t.Helper()
	client, err := newForwardProxyClient("")
	require.NoError(t, err)
	return &forwardProxyHandler{client: client, direct: direct}
}

// proxyRequest issues a proxy-style request (absolute-URL target) at the handler
// and returns the recorded response.
func proxyRequest(h *forwardProxyHandler, method, absURL string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, absURL, body) // absolute URL → r.URL.IsAbs()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestForwardProxy_FetchesAndCopies verifies an absolute-URL request is fetched
// from the (test) origin and its status/body/headers are copied back, while
// hop-by-hop headers are stripped.
func TestForwardProxy_FetchesAndCopies(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/page?q=1", r.URL.RequestURI())
		w.Header().Set("X-Origin", "yes")
		w.Header().Set("Connection", "keep-alive") // hop-by-hop, must be stripped
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "hello from origin") //nolint:errcheck // test origin handler; write error is not actionable here
	}))
	defer origin.Close()

	// origin is 127.0.0.1 → only reachable with the direct-egress LAN guard off.
	h := newProxyHandler(t, false)
	rec := proxyRequest(h, http.MethodGet, origin.URL+"/page?q=1", nil)

	require.Equal(t, http.StatusTeapot, rec.Code)
	require.Equal(t, "hello from origin", rec.Body.String())
	require.Equal(t, "yes", rec.Header().Get("X-Origin"))
	require.Empty(t, rec.Header().Get("Connection"), "hop-by-hop header must be stripped")
}

// TestForwardProxy_RejectsNonAbsolute confirms a normal (origin-form) path is
// rejected — the proxy only serves absolute-URL requests.
func TestForwardProxy_RejectsNonAbsolute(t *testing.T) {
	h := newProxyHandler(t, false)
	req := httptest.NewRequest(http.MethodGet, "/just/a/path", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestForwardProxy_RejectsConnect confirms CONNECT tunneling is refused.
func TestForwardProxy_RejectsConnect(t *testing.T) {
	h := newProxyHandler(t, false)
	req := httptest.NewRequest(http.MethodConnect, "https://example.com:443", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// TestForwardProxy_DirectGuardsLAN confirms that with direct egress (no upstream)
// a target resolving to loopback/private is refused (SSRF guard), but is allowed
// once an upstream is configured (the upstream becomes the boundary).
func TestForwardProxy_DirectGuardsLAN(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer origin.Close()

	// direct=true → the loopback origin must be blocked before any fetch.
	h := newProxyHandler(t, true)
	rec := proxyRequest(h, http.MethodGet, origin.URL+"/", nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, strings.ToLower(rec.Body.String()), "not a public address")
}

// TestHostIsPublic spot-checks the SSRF guard's address classification.
func TestHostIsPublic(t *testing.T) {
	require.False(t, hostIsPublic(""))
	require.False(t, hostIsPublic("127.0.0.1"))
	require.False(t, hostIsPublic("10.1.2.3"))
	require.False(t, hostIsPublic("192.168.1.1"))
	require.False(t, hostIsPublic("169.254.169.254")) // cloud metadata
	require.False(t, hostIsPublic("::1"))
	require.True(t, hostIsPublic("1.1.1.1"))
	require.True(t, hostIsPublic("8.8.8.8"))
}

// TestNewForwardProxyClient_UpstreamWiring confirms that configuring an upstream
// SOCKS5 installs a custom DialContext (egress routes through it), while the
// direct client leaves DialContext nil (default net dialer).
func TestNewForwardProxyClient_UpstreamWiring(t *testing.T) {
	direct, err := newForwardProxyClient("")
	require.NoError(t, err)
	require.Nil(t, direct.Transport.(*http.Transport).DialContext, "direct egress uses the default dialer")

	upstream, err := newForwardProxyClient("127.0.0.1:1080")
	require.NoError(t, err)
	require.NotNil(t, upstream.Transport.(*http.Transport).DialContext, "upstream egress must dial through SOCKS5")
}
