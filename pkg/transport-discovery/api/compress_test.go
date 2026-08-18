// Package api pkg/transport-discovery/api/compress_test.go c4-net-discovery
package api

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/transport"
	tpdiscmetrics "github.com/skycoin/skywire/pkg/transport-discovery/metrics"
)

// newCompressTestAPI returns an API backed by a memory store holding n
// registered transports.
func newCompressTestAPI(t *testing.T, n int) *API {
	t.Helper()

	ctx := context.Background()
	mock := newTestStore(t)
	nonceMock, err := httpauth.NewNonceStore(ctx, storeconfig.Config{Type: storeconfig.Memory}, "")
	require.NoError(t, err)

	for i := 0; i < n; i++ {
		entry := newTestEntry()
		sEntry := &transport.SignedEntry{Entry: entry, Signatures: [2]cipher.Sig{}}
		require.NoError(t, mock.RegisterTransport(ctx, cipher.PubKey{}, sEntry))
	}

	return New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "", "")
}

// TestCompressMiddlewareOnFreshJSON pins that endpoints which marshal fresh
// JSON per call (no pre-gzipped response cache) are compressed by the router
// middleware when the client advertises gzip.
func TestCompressMiddlewareOnFreshJSON(t *testing.T) {
	api := newCompressTestAPI(t, 40)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/all-transports/per-key-stats", nil)
	r.Header = validHeaders(t, nil)
	r.Header.Set("Accept-Encoding", "gzip")
	api.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
	require.Contains(t, w.Header().Values("Vary"), "Accept-Encoding")

	// Body must be real gzip and decode to the expected JSON shape.
	zr, err := gzip.NewReader(w.Body)
	require.NoError(t, err)
	defer zr.Close() //nolint:errcheck

	body, err := io.ReadAll(zr)
	require.NoError(t, err)

	var stats map[string]map[string]int
	require.NoError(t, json.Unmarshal(body, &stats))
	require.NotEmpty(t, stats)
}

// TestCompressMiddlewareIdentity pins that a client which does not advertise
// gzip still gets a readable, uncompressed body.
func TestCompressMiddlewareIdentity(t *testing.T) {
	api := newCompressTestAPI(t, 5)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/all-transports/per-key-stats", nil)
	r.Header = validHeaders(t, nil)
	r.Header.Set("Accept-Encoding", "identity")
	api.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Empty(t, w.Header().Get("Content-Encoding"))

	var stats map[string]map[string]int
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &stats))
}

// TestPreGzippedPathNotDoubleCompressed pins the interaction that makes the
// middleware safe to add: /all-transports and /transports/edge:<PK> serve a
// body the response cache already gzipped, and the middleware must leave it
// alone rather than gzip it a second time. A double-compressed body would
// still carry a single "Content-Encoding: gzip" and would decode -- once --
// into gzip bytes rather than JSON, so decode once and require JSON.
func TestPreGzippedPathNotDoubleCompressed(t *testing.T) {
	api := newCompressTestAPI(t, 20)

	for _, path := range []string{"/all-transports"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.Header = validHeaders(t, nil)
			r.Header.Set("Accept-Encoding", "gzip")
			api.ServeHTTP(w, r)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
			require.Equal(t, []string{"gzip"}, w.Header().Values("Content-Encoding"),
				"Content-Encoding must not be stacked")

			zr, err := gzip.NewReader(w.Body)
			require.NoError(t, err)
			defer zr.Close() //nolint:errcheck

			body, err := io.ReadAll(zr)
			require.NoError(t, err)

			// One decode must yield JSON. If the middleware had re-compressed
			// the cached gzip, this would still be gzip bytes and fail.
			var entries []*transport.Entry
			require.NoError(t, json.Unmarshal(body, &entries),
				"one gzip decode must yield JSON (body was double-compressed)")
			require.Len(t, entries, 20)
		})
	}
}

// TestAllTransportsVaryOnIdentity pins that Vary: Accept-Encoding is sent even
// when the response is served uncompressed. Without it a shared cache in front
// of TPD may store the identity body and later hand it to a gzip client.
func TestAllTransportsVaryOnIdentity(t *testing.T) {
	api := newCompressTestAPI(t, 3)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/all-transports", nil)
	r.Header = validHeaders(t, nil)
	r.Header.Set("Accept-Encoding", "identity")
	api.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Empty(t, w.Header().Get("Content-Encoding"))
	require.Contains(t, w.Header().Values("Vary"), "Accept-Encoding",
		"Vary must be set on the identity branch too")

	var entries []*transport.Entry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))
	require.Len(t, entries, 3)
}
