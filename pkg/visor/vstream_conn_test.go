package visor

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/pty"
	"github.com/skycoin/skywire/pkg/visor/logserver"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// TestSkynetPeerAddrPassesPtyGate checks that a request arriving on a direct
// skynet stream carries its caller's PK to the logserver's /pty gate. The
// loopback address the stream used to report was refused for every caller.
func TestSkynetPeerAddrPassesPtyGate(t *testing.T) {
	hv, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	api := logserver.New(logging.MustGetLogger("t"), t.TempDir(), "", nil, &visorconfig.Survey{}, false)
	wl := pty.NewMemoryWhitelist()
	require.NoError(t, wl.Add(hv))
	api.SetPtyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pty")) //nolint:errcheck
	}), wl)

	get := func(remote string) int {
		req := httptest.NewRequest("GET", "/pty", nil)
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, req)
		return rec.Code
	}
	require.Equal(t, http.StatusOK, get(skynetPeerAddr(hv).String()))
	require.Equal(t, http.StatusForbidden, get(skynetPeerAddr(other).String()))
	require.Equal(t, http.StatusForbidden, get("127.0.0.1:0"))
}
