package dmsgclient

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// TestHTTPRoundTripWireCompat proves the net/http-free httpRoundTrip speaks the
// exact HTTP/1.1 wire protocol by driving a REAL net/http server: Content-Length
// responses (GET + POST body echo), a chunked response, and an error-status body
// (whose message must map back to disc.ErrValidationWrongSequence).
func TestHTTPRoundTripWireCompat(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]string{
			"method": r.Method,
			"path":   r.URL.Path,
			"body":   string(b),
		}))
	})
	mux.HandleFunc("/chunked", func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		require.True(t, ok)
		_, err := io.WriteString(w, `{"part":`)
		require.NoError(t, err)
		fl.Flush() // forces chunked (no Content-Length)
		_, err = io.WriteString(w, `"ok"}`)
		require.NoError(t, err)
		fl.Flush()
	})
	mux.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]string{
			"message": disc.ErrValidationWrongSequence.Error(),
		}))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	// roundTrip dials a fresh conn and runs one request over it.
	roundTrip := func(method, path string, body []byte) httpResult {
		conn, err := net.Dial("tcp", host)
		require.NoError(t, err)
		defer conn.Close() //nolint:errcheck
		res, err := httpRoundTrip(conn, bufio.NewReader(conn), method, host, path, nil, body)
		require.NoError(t, err)
		return res
	}

	// GET with Content-Length response.
	res := roundTrip("GET", "/echo", nil)
	require.Equal(t, 200, res.status)
	var got map[string]string
	require.NoError(t, json.Unmarshal(res.body, &got))
	require.Equal(t, "GET", got["method"])
	require.Equal(t, "/echo", got["path"])

	// POST with a request body echoed back.
	res = roundTrip("POST", "/echo", []byte(`{"x":1}`))
	require.Equal(t, 200, res.status)
	require.NoError(t, json.Unmarshal(res.body, &got))
	require.Equal(t, "POST", got["method"])
	require.Equal(t, `{"x":1}`, got["body"])

	// Chunked Transfer-Encoding response.
	res = roundTrip("GET", "/chunked", nil)
	require.Equal(t, 200, res.status)
	require.JSONEq(t, `{"part":"ok"}`, string(res.body))

	// Error status: body message maps back to the typed sentinel.
	res = roundTrip("GET", "/fail", nil)
	require.Equal(t, 500, res.status)
	require.Equal(t, disc.ErrValidationWrongSequence, errFromBody(res.body))
}

// TestHTTPRoundTripKeepAliveReuse verifies that two sequential requests can run
// over ONE connection — the property the stream pool depends on. Pre-keep-alive
// the client sent `Connection: close`, so the server tore the connection down
// after the first response and every subsequent request paid a fresh dial (and,
// over dmsg, a full noise + PQ handshake).
func TestHTTPRoundTripKeepAliveReuse(t *testing.T) {
	var served int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"n":`+r.URL.Path[1:]+`}`)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	conn, err := net.Dial("tcp", host)
	require.NoError(t, err)
	defer conn.Close() //nolint:errcheck
	br := bufio.NewReader(conn)

	// Both round-trips share one conn and one reader.
	res1, err := httpRoundTrip(conn, br, "GET", host, "/1", nil, nil)
	require.NoError(t, err)
	require.Equal(t, 200, res1.status)
	require.JSONEq(t, `{"n":1}`, string(res1.body))
	require.True(t, res1.reusable, "framed response must be poolable")

	res2, err := httpRoundTrip(conn, br, "GET", host, "/2", nil, nil)
	require.NoError(t, err)
	require.Equal(t, 200, res2.status)
	require.JSONEq(t, `{"n":2}`, string(res2.body))
	require.True(t, res2.reusable)

	require.Equal(t, 2, served, "both requests should have been served")
}

// TestHTTPRoundTripNotReusableOnClose verifies we do NOT pool a stream the peer
// asked to close — pooling it would hand the next request a dead stream.
func TestHTTPRoundTripNotReusableOnClose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Connection", "close")
		_, _ = io.WriteString(w, "bye")
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	conn, err := net.Dial("tcp", host)
	require.NoError(t, err)
	defer conn.Close() //nolint:errcheck

	res, err := httpRoundTrip(conn, bufio.NewReader(conn), "GET", host, "/", nil, nil)
	require.NoError(t, err)
	require.False(t, res.reusable, "Connection: close must not be pooled")
}
