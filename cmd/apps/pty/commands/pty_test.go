// Package commands cmd/apps/pty/commands/pty_test.go
//
// Covers the two command constructors: newDelegateCmd (which forwards args to a
// target command) and newTCPCmd's required-flag validation. The happy path of
// newTCPCmd executes the dmsg-host server (network) and is not unit-testable
// as-is; Execute is the entrypoint.
package commands

import (
	"io"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDelegateCmdFields(t *testing.T) {
	d := newDelegateCmd("dmsg", "run over dmsg", &cobra.Command{Use: "t"})
	assert.Equal(t, "dmsg", d.Use)
	assert.Equal(t, "run over dmsg", d.Short)
	assert.True(t, d.DisableFlagParsing, "delegate must pass flags through verbatim")
}

func TestNewDelegateCmdForwardsToRunE(t *testing.T) {
	var gotArgs []string
	target := &cobra.Command{
		Use:  "t",
		RunE: func(_ *cobra.Command, args []string) error { gotArgs = args; return nil },
	}
	d := newDelegateCmd("d", "s", target)

	require.NoError(t, d.RunE(d, []string{"alpha", "beta"}))
	assert.Equal(t, []string{"alpha", "beta"}, gotArgs)
}

func TestNewDelegateCmdForwardsToRun(t *testing.T) {
	ran := false
	target := &cobra.Command{
		Use: "t",
		Run: func(_ *cobra.Command, _ []string) { ran = true },
	}
	d := newDelegateCmd("d", "s", target)

	require.NoError(t, d.RunE(d, nil))
	assert.True(t, ran, "delegate should invoke the target's Run when RunE is nil")
}

func TestNewDelegateCmdHelp(t *testing.T) {
	target := &cobra.Command{Use: "t"}
	target.SetOut(io.Discard)
	target.SetErr(io.Discard)
	d := newDelegateCmd("d", "s", target)

	// --help short-circuits to the target's Help (which returns nil).
	assert.NoError(t, d.RunE(d, []string{"--help"}))
	assert.NoError(t, d.RunE(d, []string{"-h"}))
}

func TestNewDelegateCmdParseFlagsError(t *testing.T) {
	target := &cobra.Command{Use: "t"} // defines no flags
	d := newDelegateCmd("d", "s", target)

	// An unknown flag fails target.ParseFlags before any Run.
	err := d.RunE(d, []string{"--bogus-flag"})
	assert.Error(t, err)
}

func TestNewTCPCmd(t *testing.T) {
	cmd := newTCPCmd()
	assert.Equal(t, "tcp", cmd.Use)
	require.NotNil(t, cmd.Flags().Lookup("addr"), "tcp command must expose --addr")

	// With --addr unset the command must reject before touching the server.
	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--addr is required")
}
