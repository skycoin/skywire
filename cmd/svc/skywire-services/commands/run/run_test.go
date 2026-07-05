// Package run cmd/svc/skywire-services/commands/run/run_test.go: unit tests for
// the `skywire svc run` supervisor command — the RunE branches (--list,
// missing --config, LoadFile failure, unknown service type), the command
// metadata/flags, and Execute-style invocation via cobra. The happy path
// (services.Run) boots long-lived listeners and is not unit-tested.
package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// withFlags snapshots the package flag globals and restores them after the test.
func withFlags(t *testing.T) {
	t.Helper()
	c, l := configPath, listOnly
	t.Cleanup(func() { configPath, listOnly = c, l })
}

func runE(t *testing.T) error {
	t.Helper()
	return RootCmd.RunE(RootCmd, nil)
}

// ---- RunE branches ---------------------------------------------------------

func TestRunE_List(t *testing.T) {
	withFlags(t)
	listOnly = true
	configPath = ""

	// Redirect stdout so the listing doesn't pollute test output.
	orig := os.Stdout
	_, w, _ := os.Pipe() //nolint
	os.Stdout = w
	defer func() { os.Stdout = orig; _ = w.Close() }() //nolint

	require.NoError(t, runE(t))
}

func TestRunE_MissingConfig(t *testing.T) {
	withFlags(t)
	listOnly = false
	configPath = ""

	err := runE(t)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--config is required")
}

func TestRunE_LoadFileError(t *testing.T) {
	withFlags(t)
	listOnly = false
	configPath = filepath.Join(t.TempDir(), "does-not-exist.json")

	err := runE(t)
	require.Error(t, err) // LoadFile fails on the missing file
}

func TestRunE_UnknownType(t *testing.T) {
	withFlags(t)
	listOnly = false

	path := filepath.Join(t.TempDir(), "services.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"services":[{"type":"definitely-not-a-real-service"}]}`), 0o600))
	configPath = path

	err := runE(t)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown type")
}

// ---- metadata / flags ------------------------------------------------------

func TestRootCmd_Metadata(t *testing.T) {
	require.Equal(t, "run", RootCmd.Use)
	require.Equal(t, "Run multiple deployment services in one process", RootCmd.Short)
	require.NotNil(t, RootCmd.RunE)

	require.NotNil(t, RootCmd.Flags().Lookup("config"))
	require.NotNil(t, RootCmd.Flags().Lookup("list"))
	require.Equal(t, "c", RootCmd.Flags().Lookup("config").Shorthand)
	require.Equal(t, "false", RootCmd.Flags().Lookup("list").DefValue)
}

func TestHelp_DoesNotPanic(t *testing.T) {
	defer RootCmd.SetArgs(nil)
	RootCmd.SetArgs([]string{"--help"})
	RootCmd.SetOut(os.NewFile(0, os.DevNull))
	require.NotPanics(t, func() { _ = RootCmd.Execute() }) //nolint
}
