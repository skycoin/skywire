// Package skysocksc cmd/skywire-cli/commands/proxy/tunnels_default_test.go c4-vis-cli
package skysocksc

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skyenv"
)

// TestStartTunnelsDefaultIsTwo pins the default `proxy start` aggregation
// width. Two tunnels is the measured default (bench/2026-09-16: 50 MB down
// 8.36–8.49 MB/s on two ranked routes against 6.99–7.82 on an unranked second
// tunnel and the single-route reference); it must come from
// skyenv.SkysocksClientTunnels so the CLI flag and the skysocks-client app's
// own flag defaults cannot drift apart — `proxy start` with no --tunnels and
// the hypervisor UI starting the app with no args have to mean the same thing.
func TestStartTunnelsDefaultIsTwo(t *testing.T) {
	require.Equal(t, 2, skyenv.SkysocksClientTunnels)

	f := startCmd.Flags().Lookup("tunnels")
	require.NotNil(t, f, "proxy start must still carry --tunnels")
	require.Equal(t, "2", f.DefValue)
	require.Contains(t, f.Usage, "BEST-RANKED unused route",
		"the help must say where the second tunnel is dialed")
}

// TestEffectiveTunnels: --tunnels 1 keeps its meaning (the AppDirect shortcut,
// no route group), and the single-route-group flags narrow the new default to
// one tunnel instead of colliding with it — while an explicit --tunnels >1
// stays a contradiction for the caller to reject.
func TestEffectiveTunnels(t *testing.T) {
	// Plain start: the default aggregation width survives.
	require.Equal(t, 2, effectiveTunnels(2, false, false, ""))
	// Explicit single tunnel = the AppDirect shortcut, exactly as before.
	require.Equal(t, 1, effectiveTunnels(1, true, false, ""))
	// --direct / --route with no --tunnels narrow to one route group.
	require.Equal(t, 1, effectiveTunnels(2, false, true, ""))
	require.Equal(t, 1, effectiveTunnels(2, false, false, "routes.json"))
	// Explicit --tunnels 2 with them is left alone, so the caller still errors.
	require.Equal(t, 2, effectiveTunnels(2, true, true, ""))
	require.Equal(t, 3, effectiveTunnels(3, true, false, "routes.json"))
}
