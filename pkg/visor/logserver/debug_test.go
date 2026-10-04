package logserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/flightrec"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// A whitelisted peer can fetch the Go 1.27 goroutine leak profile and the
// flight recorder's window, the two debug views `cli log pprof` names.
func TestDebugLeakProfileAndFlightRecorder(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	api := New(logging.MustGetLogger("logserver-test"), t.TempDir(), "", []cipher.PubKey{pk}, &visorconfig.Survey{}, false)
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = pk.String() + ":80"
		rr := httptest.NewRecorder()
		api.ServeHTTP(rr, req)
		return rr
	}

	rr := get("/debug/pprof/goroutineleak?debug=1")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), "goroutineleak profile")

	require.Equal(t, http.StatusNotFound, get("/debug/pprof/flightrecorder").Code, "not running yet")
	require.NoError(t, flightrec.Start(t.TempDir(), t.Logf))
	t.Cleanup(flightrec.Stop)
	rr = get("/debug/pprof/flightrecorder")
	require.Equal(t, http.StatusOK, rr.Code)
	require.Positive(t, rr.Body.Len())
}
