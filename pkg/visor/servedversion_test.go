// Package visor pkg/visor/servedversion_test.go c3-vis-core
package visor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/wasmhv/execwasm"
)

// TestExecWasmStamp pins the stamp of a module without a manifest: absent →
// empty, present → stable while untouched and different once rebuilt in
// place (size or mtime).
func TestExecWasmStamp(t *testing.T) {
	require.Empty(t, execwasm.Stamp(""))
	require.Empty(t, execwasm.Stamp(filepath.Join(t.TempDir(), "missing.wasm")))

	p := filepath.Join(t.TempDir(), "skywire.wasm")
	require.NoError(t, os.WriteFile(p, []byte("build one"), 0o600))
	t0 := time.Date(2026, 9, 8, 9, 38, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(p, t0, t0))
	s1 := execwasm.Stamp(p)
	require.Len(t, s1, 16)
	require.Equal(t, s1, execwasm.Stamp(p), "unchanged file → unchanged stamp")

	// Same size, newer mtime: a rebuild that happened to produce the same
	// number of bytes is still a new build.
	t1 := t0.Add(time.Hour)
	require.NoError(t, os.Chtimes(p, t1, t1))
	s2 := execwasm.Stamp(p)
	require.NotEqual(t, s1, s2, "rebuilt (mtime) → new stamp")

	// Different size, same mtime.
	require.NoError(t, os.WriteFile(p, []byte("build one, larger"), 0o600))
	require.NoError(t, os.Chtimes(p, t1, t1))
	require.NotEqual(t, s2, execwasm.Stamp(p), "rebuilt (size) → new stamp")
}

// TestServedVersion pins that the fingerprint a page polls is the build plus
// the module stamp, and that a page's placeholder is filled with exactly that.
func TestServedVersion(t *testing.T) {
	require.Equal(t, "abc", servedVersion("abc", ""), "no module → build alone")

	p := filepath.Join(t.TempDir(), "skywire.wasm")
	require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
	v := servedVersion("abc", p)
	require.Equal(t, "abc-"+execwasm.Stamp(p), v)

	page := []byte(`<script>window.__SKYWIRE_WASM_VERSION__="` + servedVersionToken + `";</script>`)
	got := string(renderServedVersion(page, v))
	require.Equal(t, `<script>window.__SKYWIRE_WASM_VERSION__="`+v+`";</script>`, got)
	require.NotContains(t, got, servedVersionToken)

	require.Len(t, deskAssetsStamp(), 16)
	require.Equal(t, deskAssetsStamp(), deskAssetsStamp())
}
