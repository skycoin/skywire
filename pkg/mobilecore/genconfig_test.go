// Package mobilecore pkg/mobilecore/genconfig_test.go c4-vis-core
//
// GenConfig runs `config gen` inside the process; Android runs the same
// command as a child process (`libskywire-mobile.so config gen <argv>`). The
// two must write the same config, and a second in-process run must not
// inherit anything from the first: the command keeps its state in package
// variables and was written for one run per process.
package mobilecore

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	cliconfig "github.com/skycoin/skywire/cmd/skywire-cli/commands/config"
)

// genHelperEnv makes the test binary act as `config gen`, in a process of its
// own, the way Android runs it (TestMain).
const genHelperEnv = "MOBILECORE_CONFIG_GEN_ARGS"

func TestMain(m *testing.M) {
	if raw := os.Getenv(genHelperEnv); raw != "" {
		var args []string
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			os.Exit(2)
		}
		cliconfig.RootCmd.SetArgs(append([]string{"gen"}, args...))
		if err := cliconfig.RootCmd.Execute(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// genInChild writes a config with `config gen <o.Args()>` in a child process.
func genInChild(t *testing.T, o GenOptions) {
	t.Helper()
	raw, err := json.Marshal(o.Args())
	require.NoError(t, err)
	cmd := exec.Command(os.Args[0], "-test.run=^$") //nolint:gosec // the test binary itself
	cmd.Env = append(os.Environ(), genHelperEnv+"="+string(raw))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "config gen in a child process: %s", out)
}

// readConf reads a generated config as generic JSON.
func readConf(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // a file the test wrote
	require.NoError(t, err)
	var c map[string]any
	require.NoError(t, json.Unmarshal(raw, &c))
	return c
}

// normalized drops what legitimately differs between two generations, all of
// it fresh randomness: the identity (pk, sk), the hypervisor's cookie keys and
// the LAN dmsg server's keypair (which the phone profile removes anyway).
func normalized(t *testing.T, c map[string]any) string {
	t.Helper()
	delete(c, "pk")
	delete(c, "sk")
	if hv, ok := c["hypervisor"].(map[string]any); ok {
		if ck, ok := hv["cookies"].(map[string]any); ok {
			delete(ck, "hash_key")
			delete(ck, "block_key")
		}
		if lan, ok := hv["lan_dmsg_server"].(map[string]any); ok {
			delete(lan, "pk")
			delete(lan, "sk")
		}
	}
	out, err := json.MarshalIndent(c, "", "  ")
	require.NoError(t, err)
	return string(out)
}

func pkOf(t *testing.T, path string) string {
	t.Helper()
	pk, _ := readConf(t, path)["pk"].(string) //nolint:errcheck // checked below
	require.NotEmpty(t, pk)
	return pk
}

func TestGenConfigMatchesTheCommand(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "bin")
	dirs := make([]string, 5)
	for i := range dirs {
		dirs[i] = t.TempDir()
	}
	out := func(i int) string { return filepath.Join(dirs[i], "skywire-config.json") }

	// Android's way: a child process per generation.
	genInChild(t, PhoneGenOptions(out(0), bin))
	want := normalized(t, readConf(t, out(0)))

	// In-process, first run.
	require.NoError(t, GenConfig(PhoneGenOptions(out(1), bin)))
	require.Equal(t, want, normalized(t, readConf(t, out(1))))
	pk1 := pkOf(t, out(1))

	// Regenerating the same file keeps its identity (-r).
	require.NoError(t, GenConfig(PhoneGenOptions(out(1), bin)))
	require.Equal(t, pk1, pkOf(t, out(1)), "-r did not keep the identity")

	// A run with other options, then a phone run into a fresh file: the
	// fresh file gets its own identity and none of the other run's options.
	other := PhoneGenOptions(out(2), bin)
	other.HypervisorAddr = "127.0.0.1:9999"
	other.ServeChat = true
	other.DisableApps = nil
	require.NoError(t, GenConfig(other))
	require.Contains(t, normalized(t, readConf(t, out(2))), "127.0.0.1:9999")

	require.NoError(t, GenConfig(PhoneGenOptions(out(3), bin)))
	require.Equal(t, want, normalized(t, readConf(t, out(3))), "an in-process run inherited state from the run before it")
	require.NotEqual(t, pk1, pkOf(t, out(3)), "a fresh config inherited the previous run's key")
	require.NotEqual(t, pkOf(t, out(2)), pkOf(t, out(3)), "a fresh config inherited the previous run's key")
}

func TestGenConfigErrors(t *testing.T) {
	require.Error(t, GenConfig(GenOptions{}), "no output path")

	// A Fatal inside the command (here: a dmsghttp config that does not
	// exist) comes back as an error, and the process — the app, on iOS —
	// keeps running.
	o := PhoneGenOptions(filepath.Join(t.TempDir(), "c.json"), "")
	err := cliconfig.RunGen(append(o.Args(), "--dmsgconf", filepath.Join(t.TempDir(), "missing.json")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "config gen failed (exit 1)")

	// An unknown flag is a parse error, not a Fatal.
	err = cliconfig.RunGen([]string{"--no-such-flag"})
	require.Error(t, err)
	require.True(t, strings.HasPrefix(err.Error(), "config gen:"), err.Error())

	// And the next run is unaffected by either.
	require.NoError(t, GenConfig(PhoneGenOptions(filepath.Join(t.TempDir(), "ok.json"), "")))
}
