package visor

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The streamed head keeps every value of a repeated header, drops the
// connection-level ones, names where redirects landed, and closes.
func TestBrowseStreamHead(t *testing.T) {
	final, err := url.Parse("https://example.org/landed")
	require.NoError(t, err)
	resp := &http.Response{
		StatusCode: 404,
		Header: http.Header{
			"Set-Cookie":        {"a=1", "b=2"},
			"Content-Type":      {"text/html"},
			"Transfer-Encoding": {"chunked"},
			"Connection":        {"keep-alive"},
		},
		Request: &http.Request{URL: final},
	}
	head := browseStreamHead(resp)
	require.True(t, strings.HasPrefix(head, "HTTP/1.1 404 Not Found\r\n"))
	require.Contains(t, head, "Set-Cookie: a=1\r\n")
	require.Contains(t, head, "Set-Cookie: b=2\r\n")
	require.Contains(t, head, "Content-Type: text/html\r\n")
	require.Contains(t, head, "X-Browse-Final-Url: https://example.org/landed\r\n")
	require.NotContains(t, head, "Transfer-Encoding")
	require.NotContains(t, head, "keep-alive")
	require.True(t, strings.HasSuffix(head, "Connection: close\r\n\r\n"))
}
