package got

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// --- filename.go --------------------------------------------------------

func TestGetFilename(t *testing.T) {
	require.Equal(t, "file.zip", GetFilename("http://example.com/path/file.zip"))
	require.Equal(t, "a.tar.gz", GetFilename("https://x/a.tar.gz?token=1"))
	// No extension → default.
	require.Equal(t, DefaultFileName, GetFilename("http://example.com/path/noext"))
	// Unparseable URL → default.
	require.Equal(t, DefaultFileName, GetFilename("://bad"))
}

func TestGetNameFromHeader(t *testing.T) {
	require.Equal(t, "report.pdf", getNameFromHeader(`attachment; filename="report.pdf"`))
	require.Empty(t, getNameFromHeader("not a valid header ;;;"))
	// Path traversal / separators are rejected.
	require.Empty(t, getNameFromHeader(`attachment; filename="../etc/passwd"`))
	require.Empty(t, getNameFromHeader(`attachment; filename="a/b.txt"`))
	require.Empty(t, getNameFromHeader(`attachment; filename="a\b.txt"`))
	// No filename param → empty.
	require.Empty(t, getNameFromHeader(`inline`))
}

// --- got.go: headers / urls / requests ---------------------------------

func TestParseHeaders(t *testing.T) {
	hs, err := ParseHeaders([]string{"Authorization: Bearer x", "X-A:  b "})
	require.NoError(t, err)
	require.Len(t, hs, 2)
	require.Equal(t, "Authorization", hs[0].Key)
	require.Equal(t, "Bearer x", hs[0].Value)
	require.Equal(t, "X-A", hs[1].Key)
	require.Equal(t, "b", hs[1].Value) // trimmed

	_, err = ParseHeaders([]string{"no-colon"})
	require.Error(t, err)
}

func TestNormalizeURL(t *testing.T) {
	u, err := NormalizeURL("example.com/x")
	require.NoError(t, err)
	require.Equal(t, "https://example.com/x", u)

	u, err = NormalizeURL("http://example.com")
	require.NoError(t, err)
	require.Equal(t, "http://example.com", u) // scheme preserved
}

func TestNewRequest(t *testing.T) {
	req, err := NewRequest(context.Background(), http.MethodGet, "http://x/", []Header{{"X-Test", "1"}}, nil)
	require.NoError(t, err)
	require.Equal(t, UserAgent, req.Header.Get("User-Agent"))
	require.Equal(t, "1", req.Header.Get("X-Test"))

	_, err = NewRequest(context.Background(), "BAD METHOD", "http://x/", nil, nil)
	require.Error(t, err)
}

func TestConstructors(t *testing.T) {
	require.NotNil(t, DefaultClient())

	g := New()
	require.NotNil(t, g.Client)
	require.NotNil(t, g.ctx)

	ctx := context.Background()
	g2 := NewWithContext(ctx)
	require.Equal(t, ctx, g2.ctx)
}

func TestRequest(t *testing.T) {
	var gotUA, gotHdr string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotHdr = r.Header.Get("X-Custom")
		_, _ = w.Write([]byte("response-body")) //nolint
	}))
	defer srv.Close()

	// Client nil on the receiver → DefaultClient is used (ctx must be set).
	g := &Got{ctx: context.Background()}
	buf := &bytes.Buffer{}
	resp, err := g.Request(http.MethodGet, srv.URL, []Header{{"X-Custom", "v"}}, nil, buf)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "response-body", buf.String())
	require.Equal(t, UserAgent, gotUA)
	require.Equal(t, "v", gotHdr)

	// nil output is tolerated (body discarded).
	resp, err = g.Request(http.MethodGet, srv.URL, nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestRequestError(t *testing.T) {
	g := &Got{ctx: context.Background()}
	// Bad method → request construction fails.
	_, err := g.Request("BAD METHOD", "http://x/", nil, nil, nil)
	require.Error(t, err)

	// Transport error propagates.
	g2 := &Got{ctx: context.Background(), Client: &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, context.DeadlineExceeded
		}),
	}}
	_, err = g2.Request(http.MethodGet, "http://x/", nil, nil, nil)
	require.Error(t, err)
}

// --- got.go: proxy wiring ----------------------------------------------

func TestNewWithProxy(t *testing.T) {
	ctx := context.Background()

	g, err := NewWithProxy(ctx, "socks5h://127.0.0.1:1080")
	require.NoError(t, err)
	require.NotNil(t, g.Client)

	// socks5:// → resolveLocally wrapping (still constructs cleanly).
	g, err = NewWithProxy(ctx, "socks5://127.0.0.1:1080")
	require.NoError(t, err)
	require.NotNil(t, g.Client)

	// Bare host:port back-compat.
	g, err = NewWithProxy(ctx, "127.0.0.1:1080")
	require.NoError(t, err)
	require.NotNil(t, g.Client)

	// Unsupported scheme → error from parseProxyAddr.
	_, err = NewWithProxy(ctx, "http://127.0.0.1:8080")
	require.Error(t, err)
}

func TestResolveBeforeDial(t *testing.T) {
	var gotAddr string
	inner := func(_ context.Context, _, addr string) (net.Conn, error) {
		gotAddr = addr
		return nil, nil
	}
	wrapped := resolveBeforeDial(inner)
	ctx := context.Background()

	// IP literal passes straight through (no lookup).
	_, _ = wrapped(ctx, "tcp", "127.0.0.1:80") //nolint
	require.Equal(t, "127.0.0.1:80", gotAddr)

	// Address without host:port split passes through unchanged.
	_, _ = wrapped(ctx, "tcp", "no-port-here") //nolint
	require.Equal(t, "no-port-here", gotAddr)

	// Hostname is resolved before dialing → inner sees an IP:port.
	_, _ = wrapped(ctx, "tcp", "localhost:80") //nolint
	require.NotEqual(t, "localhost:80", gotAddr)
	require.True(t, strings.HasSuffix(gotAddr, ":80"))
	host, _, err := net.SplitHostPort(gotAddr)
	require.NoError(t, err)
	require.NotNil(t, net.ParseIP(host)) // resolved to a real IP
}
