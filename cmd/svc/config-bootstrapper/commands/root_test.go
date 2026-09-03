// Package commands cmd/svc/config-bootstrapper/commands/root_test.go: unit
// tests for the testable surface of the config-bootstrapper root command — the
// JSON example helpers, readConfig (missing / valid / malformed file), the
// command metadata and registered flags, and Execute's --help path. The
// RootCmd.Run closure boots svcmode listeners and ends in logger.Fatal on
// error, so its setup is exercised in a subprocess that fails fast at
// ResolveMode with an invalid --mode.
package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/skycoin/skywire/pkg/cmdutil"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/deployment/conf/api"
	"github.com/skycoin/skywire/pkg/logging"
)

func testLog() *logging.Logger { return logging.MustGetLogger("cb-test") }

// ---- exampleJSON / generateExamples ----------------------------------------

func TestExampleJSON(t *testing.T) {
	out := cmdutil.ExampleJSON(map[string]string{"version": "v1.3.29"})
	require.Contains(t, out, "v1.3.29")

	// Unmarshalable value (channel) → json.MarshalIndent fails → "".
	require.Equal(t, "", cmdutil.ExampleJSON(make(chan int)))
}

func TestGenerateExamples(t *testing.T) {
	out := generateExamples()
	require.NotEmpty(t, out)
	require.Contains(t, out, "Response Examples")
	require.Contains(t, out, "GET /health")
	require.Contains(t, out, "dmsg_discovery")
	require.Contains(t, out, "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5")
}

// ---- readConfig ------------------------------------------------------------

func TestReadConfig_MissingFileReturnsEmpty(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.json")
	cfg := readConfig(testLog(), missing)
	require.Equal(t, api.Config{}, cfg)
}

func TestReadConfig_ValidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"stun_servers":["stun.example.com:3478"]}`), 0o600))

	cfg := readConfig(testLog(), path)
	require.Equal(t, []string{"stun.example.com:3478"}, cfg.StunServers)
}

// ---- command metadata / flags ----------------------------------------------

func TestRootCmd_Metadata(t *testing.T) {
	require.Equal(t, "Config Bootstrap Server for skywire", RootCmd.Short)
	require.NotNil(t, RootCmd.Run)
	require.NotEmpty(t, RootCmd.Use)
	require.Contains(t, RootCmd.Long, "Bootstrap configuration")
	require.True(t, RootCmd.SilenceErrors)
	require.True(t, RootCmd.SilenceUsage)
}

func TestRootCmd_Flags(t *testing.T) {
	for _, name := range []string{
		"addr", "pprof", "tag", "config", "domain", "dmsg-disc",
		"dmsg-disc-dmsg", "sk", "keyfile", "dmsg-port", "dmsg-server-type", "mode",
	} {
		require.NotNil(t, RootCmd.Flags().Lookup(name), "flag %q should be registered", name)
	}

	require.Equal(t, ":9082", RootCmd.Flags().Lookup("addr").DefValue)
	require.Equal(t, "config_bootstrapper", RootCmd.Flags().Lookup("tag").DefValue)
	require.Equal(t, "./config.json", RootCmd.Flags().Lookup("config").DefValue)
	require.Equal(t, "skywire.skycoin.com", RootCmd.Flags().Lookup("domain").DefValue)
	require.Equal(t, "a", RootCmd.Flags().Lookup("addr").Shorthand)
}

// ---- Execute (--help) ------------------------------------------------------

func TestExecute_Help(t *testing.T) {
	defer RootCmd.SetArgs(nil)
	RootCmd.SetArgs([]string{"--help"})
	RootCmd.SetOut(os.NewFile(0, os.DevNull))
	require.NotPanics(t, Execute)
}

// ---- RootCmd.Run via subprocess --------------------------------------------

// TestRun_Subprocess executes the Run closure in a child process with an
// invalid --mode so svcmode.ResolveMode fails and the closure calls
// logger.Fatal, exiting non-zero. This drives the closure's setup (buildinfo,
// logger, pprof, readConfig, keyfile skip, pubkey, api.New, SignalContext)
// without binding listeners.
func TestRun_Subprocess(t *testing.T) {
	if os.Getenv("CB_RUN_SUBPROCESS") == "1" {
		RootCmd.SetArgs([]string{"--mode", "not-a-real-mode"})
		Execute()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestRun_Subprocess") //nolint:gosec
	cmd.Env = append(os.Environ(), "CB_RUN_SUBPROCESS=1")
	err := cmd.Run()

	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, "Run should exit non-zero via logger.Fatal")
	require.False(t, exitErr.Success())
}
