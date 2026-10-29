//go:build client_e2e
// +build client_e2e

package nativee2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// wasmVisorPK is the key in testdata/visorW.json.
const wasmVisorPK = "0228fa105cf0988b1be1056d581a40fa67af37312e9fafc9da6a7bb8055160a967"

// TestWasmVisor runs the js/wasm build of the visor, the one a browser tab
// runs, under Node against the native deployment. It has no sockets, so it
// reaches the dmsg server over WebSocket only (dmsg carriers ["ws"]).
func TestWasmVisor(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the wasm visor e2e is only verified on linux")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed, needed to run the js/wasm visor")
	}
	out, err := exec.Command("go", "env", "GOROOT").Output()
	require.NoError(t, err)
	wasmExec := filepath.Join(strings.TrimSpace(string(out)), "lib", "wasm", "wasm_exec_node.js")
	require.FileExists(t, wasmExec)

	module := buildWasmVisor(t)
	require.NoError(t, startProc("visorW", node, wasmExec, module, "visor", "-c", "visorW.json"))
	t.Cleanup(func() {
		if t.Failed() {
			dumpLog("visorW")
		}
	})

	// It registered in dmsg discovery through a session to the native dmsg server.
	var entry string
	require.Eventually(t, func() bool {
		entry, _ = httpGet(dmsgDiscURL + "/dmsg-discovery/entry/" + wasmVisorPK)
		return strings.Contains(entry, "delegated_servers")
	}, 120*time.Second, 3*time.Second, "wasm visor not registered in dmsg discovery (last=%.200q)", entry)

	// A native visor dials it over dmsg and holds a transport to it.
	require.Eventually(t, func() bool {
		_, err := cliT(60*time.Second, "tp", "add", wasmVisorPK, "-t", "dmsg", "--rpc", rpcB)
		return err == nil
	}, 120*time.Second, 5*time.Second, "visorB could not add a dmsg transport to the wasm visor")
	tps, err := cli("tp", "ls", "--rpc", rpcB)
	require.NoError(t, err)
	t.Logf("visorB transports:\n%s", tps)
	require.Contains(t, tps, wasmVisorPK)
}

// buildWasmVisor returns the js/wasm module, prebuilt as skywire.wasm in
// SKYWIRE_NATIVEE2E_BIN or built here with the tags of make build-wasm.
func buildWasmVisor(t *testing.T) string {
	if pre := os.Getenv("SKYWIRE_NATIVEE2E_BIN"); pre != "" {
		if p := filepath.Join(pre, "skywire.wasm"); fileExists(p) {
			return p
		}
	}
	root, err := repoRoot()
	require.NoError(t, err)
	module := filepath.Join(env.work, "skywire.wasm")
	cmd := exec.Command("go", "build", "-tags", "withoutsystray withoutgotop", "-o", module, ".")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	b, err := cmd.CombinedOutput()
	require.NoError(t, err, "build the js/wasm visor: %s", b)
	return module
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
