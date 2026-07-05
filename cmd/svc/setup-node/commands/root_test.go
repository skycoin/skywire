// Package commands cmd/svc/setup-node/commands/root_test.go: unit tests for the
// testable surface of the setup-node root command — the JSON example helpers,
// the command metadata/flags wired up in init(), and Execute's help path. The
// RootCmd.Run / checkHealthCmd.Run closures boot a dmsg-serving node (or call
// os.Exit) and are not unit-testable.
package commands

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExampleJSON(t *testing.T) {
	out := exampleJSON(map[string]string{"k": "v"})
	require.Contains(t, out, "\"k\"")
	require.Contains(t, out, "v")

	// Unmarshalable value (channel) → json.MarshalIndent fails → "".
	require.Equal(t, "", exampleJSON(make(chan int)))
}

func TestGenerateExamples(t *testing.T) {
	out := generateExamples()
	require.NotEmpty(t, out)
	require.Contains(t, out, "Example Config:")
	require.Contains(t, out, "Generate Keys:")
	// The rendered SetupConfig carries the prod transport-discovery URL.
	require.Contains(t, out, "transport_discovery")
}

func TestRootCmd_Metadata(t *testing.T) {
	require.Equal(t, "Route Setup Node for skywire", RootCmd.Short)
	require.NotNil(t, RootCmd.Run)

	for _, name := range []string{"metrics", "pprofmode", "pprofaddr", "tag", "stdin"} {
		require.NotNil(t, RootCmd.Flags().Lookup(name), "flag %q should be registered", name)
	}
	require.Equal(t, "localhost:6060", RootCmd.Flags().Lookup("pprofaddr").DefValue)
	require.Equal(t, "i", RootCmd.Flags().Lookup("stdin").Shorthand)
}

func TestCheckHealthCmd_Registered(t *testing.T) {
	// init() adds checkHealthCmd as a subcommand of RootCmd.
	var found bool
	for _, c := range RootCmd.Commands() {
		if c.Name() == "health" {
			found = true
			require.Equal(t, "Health check of route setup node", c.Short)
		}
	}
	require.True(t, found, "health subcommand should be registered")
}

func TestExecute_Help(t *testing.T) {
	defer RootCmd.SetArgs(nil)
	RootCmd.SetArgs([]string{"--help"})
	RootCmd.SetOut(os.NewFile(0, os.DevNull))
	RootCmd.SetErr(os.NewFile(0, os.DevNull))
	require.NotPanics(t, Execute)
}
