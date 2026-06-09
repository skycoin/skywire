// Package dmsghttp_test pkg/dmsghttp/withdebug_test.go
package dmsghttp_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
)

func TestWithDebug_RoutesAndGates(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "service-handler") //nolint:errcheck
	})
	logSource := func() []byte { return []byte("recent-log-line\n") }
	h := dmsghttp.WithDebug(next, []cipher.PubKey{pk}, logSource)

	// Non-/debug path → service handler, no auth required.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "anything:1"
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "service-handler")

	// /debug/log with a whitelisted PK → serves logSource().
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/debug/log", nil)
	req.RemoteAddr = pk.String() + ":1234"
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "recent-log-line")

	// /debug/pprof with a NON-whitelisted PK → 401.
	other, _ := cipher.GenerateKeyPair()
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	req.RemoteAddr = other.String() + ":1234"
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
