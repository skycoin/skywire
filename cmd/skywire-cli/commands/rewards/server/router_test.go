// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/router_test.go
package clirewardsserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestRouter builds the whole router, which panics on a conflicting pattern,
// and checks the routing rules that differ between gin and http.ServeMux.
func TestRouter(t *testing.T) {
	wd = t.TempDir()
	pk, _ := cipher.GenerateKeyPair()
	wlkeys = []cipher.PubKey{pk}
	disableTpVizAPI = true
	t.Cleanup(func() { wlkeys = nil })

	pkDir := filepath.Join(wd, "log_backups", pk.Hex())
	require.NoError(t, os.MkdirAll(pkDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(pkDir, "node-info.json"), []byte(`{}`), 0o600))

	h := buildRouter()
	do := func(method, path, remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if remote != "" {
			req.RemoteAddr = remote
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	require.Equal(t, http.StatusNoContent, do(http.MethodGet, "/204", "").Code)
	require.Equal(t, http.StatusNotFound, do(http.MethodGet, "/no-such-page", "").Code)
	require.Equal(t, http.StatusMethodNotAllowed, do(http.MethodPost, "/204", "").Code)

	file := "/log-collection/file/" + pk.Hex() + "/node-info.json"
	require.Equal(t, http.StatusUnauthorized, do(http.MethodGet, file, "127.0.0.1:1").Code)
	w := do(http.MethodGet, file, pk.Hex()+":80")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "application/json", w.Header().Get("Content-Type"))
	require.Equal(t, `{}`, w.Body.String())

	// ServeMux unescapes path values, so %2F must not reach a file path.
	require.Equal(t, http.StatusBadRequest, do(http.MethodGet, "/log-collection/file/..%2F..%2F/node-info.json", pk.Hex()+":80").Code)
	require.Equal(t, http.StatusBadRequest, do(http.MethodGet, "/node-info/..%2F..%2Fetc", pk.Hex()+":80").Code)

	w = do(http.MethodGet, "/skycoin-rewards/hist/2026-1-1/json", "")
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "invalid date format")
}
