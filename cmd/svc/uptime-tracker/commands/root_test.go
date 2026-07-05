// Package commands cmd/svc/uptime-tracker/commands/root_test.go: unit tests for
// the testable surface of the uptime-tracker root command — the JSON example
// helpers, the command metadata/flags wired up in init(), and Execute()'s
// non-error path via --help.
package commands

import (
	"bytes"
	"strings"
	gotesting "testing"

	"github.com/stretchr/testify/require"
)

func TestExampleJSON(t *gotesting.T) {
	out := exampleJSON(map[string]string{"version": "v1.3.29"})
	require.Contains(t, out, "v1.3.29")
	require.Contains(t, out, "version")

	// An unmarshalable value (channel) makes json.MarshalIndent fail →
	// exampleJSON returns "".
	require.Equal(t, "", exampleJSON(make(chan int)))
}

func TestGenerateExamples(t *gotesting.T) {
	out := generateExamples()
	require.NotEmpty(t, out)
	require.Contains(t, out, "Request/Response Examples:")
	require.Contains(t, out, "GET /health")
	require.Contains(t, out, "GET /security/nonces/{pk}")
	// Example PKs from the helper should be rendered into the output.
	require.Contains(t, out, "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5")
}

func TestRootCmd_Metadata(t *gotesting.T) {
	require.NotNil(t, RootCmd)
	require.Equal(t, "Uptime Tracker Server for skywire", RootCmd.Short)
	require.Contains(t, RootCmd.Long, "Uptime Tracker Server")
	require.NotNil(t, RootCmd.Run)
}

func TestRootCmd_FlagsRegistered(t *gotesting.T) {
	// A representative sample of the flags wired up in init().
	for _, name := range []string{
		"addr", "private-addr", "metrics", "redis", "redis-pool-size",
		"pg-host", "pg-port", "testing", "dmsg-port", "mode", "keyfile",
	} {
		require.NotNil(t, RootCmd.Flags().Lookup(name), "flag %q should be registered", name)
	}

	// Default values set by init().
	addrFlag := RootCmd.Flags().Lookup("addr")
	require.Equal(t, ":9096", addrFlag.DefValue)
	require.Equal(t, "a", addrFlag.Shorthand)
}

func TestExecute_Help(t *gotesting.T) {
	// --help short-circuits cobra before the Run closure, so Execute()
	// returns without booting any servers or calling os.Exit.
	var buf bytes.Buffer
	RootCmd.SetOut(&buf)
	RootCmd.SetErr(&buf)
	RootCmd.SetArgs([]string{"--help"})
	t.Cleanup(func() { RootCmd.SetArgs(nil) })

	require.NotPanics(t, Execute)
	require.True(t, strings.Contains(buf.String(), "Uptime Tracker Server") ||
		strings.Contains(buf.String(), "Usage"))
}
