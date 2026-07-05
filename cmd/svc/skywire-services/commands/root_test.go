// Package commands cmd/svc/skywire-services/commands/root_test.go: unit tests
// for the skywire-services aggregator root command — that init() wires up the
// per-service subcommands under their short aliases, the root metadata, and
// Execute's --help path. The subcommands themselves are covered by their own
// packages' tests.
package commands

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRootCmd_Metadata(t *testing.T) {
	require.Equal(t, "Skywire services", RootCmd.Short)
	require.NotEmpty(t, RootCmd.Use)
	require.True(t, RootCmd.SilenceErrors)
	require.True(t, RootCmd.SilenceUsage)
}

func TestRootCmd_SubcommandsRegistered(t *testing.T) {
	got := map[string]bool{}
	for _, c := range RootCmd.Commands() {
		got[c.Use] = true
	}
	// init() renames each aggregated service to a short alias.
	for _, name := range []string{
		"tpd", "tps", "ar", "rf", "confbs", "conf", "se",
		"ut", "sd", "sn", "nm", "ip", "stun", "run",
	} {
		require.True(t, got[name], "subcommand %q should be registered", name)
	}
}

func TestExecute_Help(t *testing.T) {
	defer RootCmd.SetArgs(nil)
	RootCmd.SetArgs([]string{"--help"})
	RootCmd.SetOut(os.NewFile(0, os.DevNull))
	require.NotPanics(t, Execute)
}
