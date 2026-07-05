// Package commands cmd/svc/stun-server/commands/root_test.go: unit tests for
// the stun-server root command. Covers the registered flags and their
// defaults, the command metadata, Execute's --help path, and the RootCmd.Run
// closure. Run ends in logger.Fatal (os.Exit) when stun.New(...).Run returns
// an error, and its success path would bind four UDP sockets on two distinct
// IPs, so the closure is exercised in a subprocess where the Fatal exit is
// observable without binding real sockets.
package commands

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---- flags / metadata ------------------------------------------------------

func TestRootCmd_Metadata(t *testing.T) {
	require.Equal(t, "STUN server for skywire", RootCmd.Short)
	require.NotNil(t, RootCmd.Run)
	require.NotEmpty(t, RootCmd.Use)
	require.Contains(t, RootCmd.Long, "RFC 3489")
	require.True(t, RootCmd.SilenceErrors)
	require.True(t, RootCmd.SilenceUsage)
}

func TestRootCmd_Flags(t *testing.T) {
	for _, name := range []string{"primary-ip", "secondary-ip", "port", "alt-port", "loglvl", "tag"} {
		require.NotNil(t, RootCmd.Flags().Lookup(name), "flag %q should be registered", name)
	}

	require.Equal(t, "3478", RootCmd.Flags().Lookup("port").DefValue)
	require.Equal(t, "3479", RootCmd.Flags().Lookup("alt-port").DefValue)
	require.Equal(t, "info", RootCmd.Flags().Lookup("loglvl").DefValue)
	require.Equal(t, "stun", RootCmd.Flags().Lookup("tag").DefValue)
	require.Equal(t, "", RootCmd.Flags().Lookup("primary-ip").DefValue)
	require.Equal(t, "", RootCmd.Flags().Lookup("secondary-ip").DefValue)

	// -l is the shorthand for --loglvl.
	require.Equal(t, "l", RootCmd.Flags().Lookup("loglvl").Shorthand)
}

// ---- Execute (--help) ------------------------------------------------------

func TestExecute_Help(t *testing.T) {
	defer RootCmd.SetArgs(nil)
	RootCmd.SetArgs([]string{"--help"})
	RootCmd.SetOut(os.NewFile(0, os.DevNull))
	require.NotPanics(t, Execute)
}

// ---- RootCmd.Run via subprocess --------------------------------------------

// TestRun_Subprocess executes the Run closure in a child process. With both
// IPs empty, stun.New(...).Run returns the "required" error and the closure
// calls logger.Fatal, exiting non-zero — covering the closure end to end
// without binding UDP sockets.
func TestRun_Subprocess(t *testing.T) {
	if os.Getenv("STUN_RUN_SUBPROCESS") == "1" {
		RootCmd.SetArgs([]string{}) // defaults: empty primary/secondary IPs
		Execute()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestRun_Subprocess") //nolint:gosec
	cmd.Env = append(os.Environ(), "STUN_RUN_SUBPROCESS=1")
	err := cmd.Run()

	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, "Run should exit non-zero via logger.Fatal")
	require.False(t, exitErr.Success())
}
