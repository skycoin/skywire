// Package commands cmd/svc/sw-env/commands/root_test.go: unit tests for the
// sw-env root command — the root Run closure for each environment flag
// (public/local/docker), the visor/dmsg/setup subcommand Run closures (output
// captured from stdout and asserted as valid JSON), subcommand registration and
// flag metadata, and Execute's --help path.
package commands

import (
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

// resetFlags restores the env selection flags after a test mutates them.
func resetFlags(t *testing.T) {
	t.Helper()
	p, l, d, n := publicFlag, localFlag, dockerFlag, dockerNetwork
	t.Cleanup(func() { publicFlag, localFlag, dockerFlag, dockerNetwork = p, l, d, n })
}

// ---- root Run closure ------------------------------------------------------

func TestRootRun_Public(t *testing.T) {
	resetFlags(t)
	publicFlag, localFlag, dockerFlag = true, false, false

	out := captureStdout(t, func() { RootCmd.Run(RootCmd, nil) })
	require.True(t, json.Valid([]byte(out)), "public env should be valid JSON")
}

func TestRootRun_Local(t *testing.T) {
	resetFlags(t)
	publicFlag, localFlag, dockerFlag = false, true, false

	out := captureStdout(t, func() { RootCmd.Run(RootCmd, nil) })
	require.True(t, json.Valid([]byte(out)), "local env should be valid JSON")
}

func TestRootRun_Docker(t *testing.T) {
	resetFlags(t)
	publicFlag, localFlag, dockerFlag = false, false, true
	dockerNetwork = "TESTNET"

	out := captureStdout(t, func() { RootCmd.Run(RootCmd, nil) })
	require.True(t, json.Valid([]byte(out)), "docker env should be valid JSON")
	require.Contains(t, out, "TESTNET")
}

func TestRootRun_NoFlag(t *testing.T) {
	resetFlags(t)
	publicFlag, localFlag, dockerFlag = false, false, false

	// No flag selected → switch falls through, nothing printed.
	out := captureStdout(t, func() { RootCmd.Run(RootCmd, nil) })
	require.Empty(t, out)
}

// ---- subcommand Run closures -----------------------------------------------

func TestSubcommandRun(t *testing.T) {
	for _, c := range []struct {
		name string
		cmd  *cobra.Command
	}{
		{"visor", visorCmd},
		{"dmsg", dmsgCmd},
		{"setup", setupCmd},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := captureStdout(t, func() { c.cmd.Run(c.cmd, nil) })
			require.True(t, json.Valid([]byte(out)), "%s config should be valid JSON", c.name)
		})
	}
}

// ---- metadata / registration / Execute -------------------------------------

func TestRootCmd_Metadata(t *testing.T) {
	require.Equal(t, "skywire environment generator", RootCmd.Short)
	require.NotNil(t, RootCmd.Run)

	got := map[string]bool{}
	for _, sub := range RootCmd.Commands() {
		got[sub.Use] = true
	}
	for _, name := range []string{"visor", "dmsg", "setup"} {
		require.True(t, got[name], "subcommand %q should be registered", name)
	}

	for _, name := range []string{"public", "local", "docker", "network"} {
		require.NotNil(t, RootCmd.Flags().Lookup(name), "flag %q should be registered", name)
	}
	require.Equal(t, "SKYNET", RootCmd.Flags().Lookup("network").DefValue)
	require.Equal(t, "p", RootCmd.Flags().Lookup("public").Shorthand)
}

func TestExecute_Help(t *testing.T) {
	defer RootCmd.SetArgs(nil)
	RootCmd.SetArgs([]string{"--help"})
	RootCmd.SetOut(os.NewFile(0, os.DevNull))
	require.NotPanics(t, Execute)
}
