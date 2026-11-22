// Package rewardsserver pkg/deployment/rewards/server/cliexec_test.go c3-app-rewards
package rewardsserver

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestSurveyPageUsesTheWorkDir runs the per-visor survey page from another
// working directory, as in a visor whose unit sets none: the tree command
// must get the work dir's log_backups, not a path relative to the process.
func TestSurveyPageUsesTheWorkDir(t *testing.T) {
	wd = t.TempDir()
	disableTpVizAPI = true
	skycoinNode = "http://127.0.0.1:1"
	t.Cleanup(func() { skycoinNode = "" })
	pk, _ := cipher.GenerateKeyPair()
	pkDir := filepath.Join(wd, "log_backups", pk.Hex())
	require.NoError(t, os.MkdirAll(pkDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(pkDir, "node-info.json"), []byte(`{}`), 0o600))

	var got []string
	run := runSkywireCLI
	runSkywireCLI = func(args ...string) ([]byte, error) {
		got = args
		return []byte("tree"), nil
	}
	t.Cleanup(func() { runSkywireCLI = run })

	rpcAddr, rpcAddrSet := os.LookupEnv("RPC_ADDR")
	h := buildRouter()
	v, set := os.LookupEnv("RPC_ADDR")
	require.Equal(t, rpcAddrSet, set, "buildRouter must not set RPC_ADDR on the process")
	require.Equal(t, rpcAddr, v)

	t.Chdir(t.TempDir())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/log-collection/tree/"+pk.Hex(), nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "tree")

	i := slices.Index(got, "-d")
	require.GreaterOrEqual(t, i, 0, "args %q", got)
	dir := got[i+1]
	require.True(t, filepath.IsAbs(dir), dir)
	require.Equal(t, filepath.Join(wd, "log_backups"), dir)
	require.DirExists(t, filepath.Join(dir, pk.Hex()))
}

func TestSkycoinCLIGetsTheNodeInItsOwnEnvironment(t *testing.T) {
	skycoinNode = "http://127.0.0.1:6420"
	t.Cleanup(func() { skycoinNode = "" })
	out, err := skycoinCLI().Exec(os.Args[0] + " -test.run=^TestPrintRPCAddr$").String()
	require.NoError(t, err)
	require.Contains(t, out, "RPC_ADDR=http://127.0.0.1:6420")
	_, set := os.LookupEnv("RPC_ADDR")
	if set {
		require.NotEqual(t, "http://127.0.0.1:6420", os.Getenv("RPC_ADDR"))
	}
}

// TestPrintRPCAddr is the child process of the test above.
func TestPrintRPCAddr(t *testing.T) {
	if v := os.Getenv("RPC_ADDR"); v != "" {
		os.Stdout.WriteString("RPC_ADDR=" + v + "\n") //nolint:errcheck,gosec
	}
}
