// Package visor pkg/visor/wasmmodule_test.go c3-vis-core
package visor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	"github.com/skycoin/skywire/pkg/wasmhv/execwasm"
)

// wasmSource fakes the source visor's /wasm/ routes. module is what it
// serves; manifest describes good, so a different module fails the check.
type wasmSource struct {
	module, good []byte
	hits         atomic.Int32
}

func (s *wasmSource) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.hits.Add(1)
	switch r.URL.Path {
	case wasmModuleRoute + execwasm.ManifestName:
		sum := sha256.Sum256(s.good)
		fmt.Fprintf(w, `{"version":"v2","revision":"r2","sha256":%q,"size":%d}`, hex.EncodeToString(sum[:]), len(s.good)) //nolint:errcheck,gosec // a test fake
	case wasmModuleRoute + execwasm.ModuleName:
		_, _ = w.Write(s.module) //nolint:errcheck,gosec // a test fake
	default:
		http.NotFound(w, r)
	}
}

func newRefresher(t *testing.T, src *wasmSource) *wasmModuleRefresher {
	t.Helper()
	srv := httptest.NewServer(src)
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")
	pk, _ := cipher.GenerateKeyPair()
	path := filepath.Join(t.TempDir(), execwasm.ModuleName)
	require.NoError(t, os.WriteFile(path, []byte("old module"), 0o600))
	return &wasmModuleRefresher{
		path:   path,
		source: pk,
		// Every dial goes to the fake source, as a dmsg dial goes to the pk.
		client: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}}},
		log: logging.MustGetLogger("wasmmodule_test"),
	}
}

func TestWasmModuleRefresh(t *testing.T) {
	src := &wasmSource{module: []byte("new module"), good: []byte("new module")}
	m := newRefresher(t, src)

	m.refresh(context.Background())
	got, err := os.ReadFile(m.path)
	require.NoError(t, err)
	require.Equal(t, "new module", string(got))
	mf, err := execwasm.ReadManifest(m.path)
	require.NoError(t, err)
	require.Equal(t, "v2", mf.Version)
	require.Equal(t, int32(2), src.hits.Load(), "manifest, then module")

	// Within wasmModuleCheckEvery the source is not asked again.
	m.refresh(context.Background())
	require.Equal(t, int32(2), src.hits.Load())

	// Once due, a module that is already current is not fetched again.
	m.lastCheck = m.lastCheck.Add(-wasmModuleCheckEvery)
	m.refresh(context.Background())
	require.Equal(t, int32(3), src.hits.Load(), "manifest only")
}

func TestWasmModuleRefreshRejectsAMismatch(t *testing.T) {
	src := &wasmSource{module: []byte("tampered!!"), good: []byte("new module")}
	m := newRefresher(t, src)

	m.refresh(context.Background())
	got, err := os.ReadFile(m.path)
	require.NoError(t, err)
	require.Equal(t, "old module", string(got), "a module that does not match its manifest is not installed")
	_, err = os.Stat(execwasm.ManifestPath(m.path))
	require.True(t, os.IsNotExist(err))
	left, err := filepath.Glob(filepath.Join(filepath.Dir(m.path), ".skywire.wasm.gz-*"))
	require.NoError(t, err)
	require.Empty(t, left, "the download is cleaned up")
}

func TestWasmModuleRefreshNil(t *testing.T) {
	var m *wasmModuleRefresher
	m.refresh(context.Background())
	m.await(context.Background())
}

// A hypervisor whose visor can refresh the module serves the desk before it
// has one, since opening the desk is what fetches it.
func TestExecModuleBeforeTheFirstRefresh(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	hv := &Hypervisor{visor: &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{PK: pk}}}}
	hv.visor.wasmModuleOnce.Do(func() {})
	if _, ok := hv.execModule(); ok {
		t.Skip("a module is installed beside the test binary")
	}

	hv = &Hypervisor{visor: &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{PK: pk}}}}
	hv.visor.wasmModuleOnce.Do(func() { hv.visor.wasmModuleRef = &wasmModuleRefresher{} })
	path, ok := hv.execModule()
	require.True(t, ok)
	require.Equal(t, execwasm.DefaultPath(), path)
}

// TestWasmModuleSource pins that the deployment names the source, a config
// overrides it, and a deployment without one means no refresh.
func TestWasmModuleSource(t *testing.T) {
	saved := deployment.Prod.WasmModuleSource
	t.Cleanup(func() { deployment.Prod.WasmModuleSource = saved })

	dep, _ := cipher.GenerateKeyPair()
	own, _ := cipher.GenerateKeyPair()
	deployment.Prod.WasmModuleSource = dep.Hex()

	pk, err := wasmModuleSource(nil)
	require.NoError(t, err)
	require.Equal(t, dep, pk)

	pk, err = wasmModuleSource(&visorconfig.HypervisorConfig{WasmModuleSource: own.Hex()})
	require.NoError(t, err)
	require.Equal(t, own, pk)

	deployment.Prod.WasmModuleSource = ""
	pk, err = wasmModuleSource(nil)
	require.NoError(t, err)
	require.True(t, pk.Null())

	_, err = wasmModuleSource(&visorconfig.HypervisorConfig{WasmModuleSource: "not-a-key"})
	require.Error(t, err)
}
