package visor

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRuntimeLogsNilLogstore is a regression guard: a runtime-log RPC that
// arrives before the logstore is wired (the startup window, when the RPC has
// bound but NewVisor's early wiring hasn't run in some construction path) must
// return an empty result, NOT segfault the whole visor on a nil-pointer deref.
// A `cli visor log` during startup previously took the process down this way.
func TestRuntimeLogsNilLogstore(t *testing.T) {
	v := &Visor{} // logstore is nil

	require.NotPanics(t, func() {
		out, err := v.RuntimeLogs()
		require.NoError(t, err)
		require.Equal(t, "[]", out)
	})

	require.NotPanics(t, func() {
		delta, err := v.RuntimeLogsSince(0)
		require.NoError(t, err)
		require.Empty(t, delta.Entries)
		require.Zero(t, delta.Latest)
		require.Zero(t, delta.Dropped)
	})
}
