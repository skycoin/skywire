// Package commands cmd/apps/skysocks-client/commands/tunnels_default_test.go c4-app-proxy
package commands

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skyenv"
)

// TestTunnelsDefaultIsTwo pins BOTH of the app's --tunnels defaults to the one
// shared constant. The app is launched two ways — as a standalone binary
// (cobra, RootCmd) and by the visor with a persisted arg list
// (clientConfig.parseArgs' own flag set) — and the hypervisor UI passes no
// --tunnels at all, so the two defaults drifting apart would silently give the
// GUI a different aggregation width than the CLI (the CLI/GUI parity rule).
func TestTunnelsDefaultIsTwo(t *testing.T) {
	require.Equal(t, 2, skyenv.SkysocksClientTunnels)

	f := RootCmd.Flags().Lookup("tunnels")
	require.NotNil(t, f)
	require.Equal(t, "2", f.DefValue)

	c := &clientConfig{}
	require.NoError(t, c.parseArgs([]string{"--srv", "02328671a5c84852bba18478fa63d8193f86c2ddbfd00273f78f183b022c664af7"}))
	require.Equal(t, int64(skyenv.SkysocksClientTunnels), c.tunnels,
		"the visor-launched path must default to the same width as the CLI")

	// An explicit single tunnel still means one tunnel (the AppDirect shortcut).
	c = &clientConfig{}
	require.NoError(t, c.parseArgs([]string{"--tunnels", "1"}))
	require.Equal(t, int64(1), c.tunnels)
}
