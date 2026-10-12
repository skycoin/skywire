package logserver

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// Any visor serves its js/wasm module and manifest as their bytes, so another
// visor can check them against each other; one without a module answers 404.
func TestWasmModuleRoutes(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	api := New(logging.MustGetLogger("logserver-test"), t.TempDir(), "", []cipher.PubKey{pk}, &visorconfig.Survey{}, false)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Accept-Encoding", "gzip")
		api.ServeHTTP(rec, r)
		return rec
	}

	require.Equal(t, 404, get("/wasm/skywire.wasm.json").Code)

	dir := t.TempDir()
	module := filepath.Join(dir, "skywire.wasm.gz")
	require.NoError(t, os.WriteFile(module, []byte("\x1f\x8bgzipped"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "skywire.wasm.json"), []byte(`{"version":"v"}`), 0o600))
	api.SetWasmModule(module)

	rec := get("/wasm/skywire.wasm.gz")
	require.Equal(t, 200, rec.Code)
	require.Equal(t, "\x1f\x8bgzipped", rec.Body.String())
	require.Empty(t, rec.Header().Get("Content-Encoding"))
	require.Equal(t, `{"version":"v"}`, get("/wasm/skywire.wasm.json").Body.String())
	require.Equal(t, 404, get("/wasm/other").Code)
}
