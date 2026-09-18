// Package skysocksc cmd/skywire-cli/commands/proxy/settings_test.go
package skysocksc

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

func TestApplySettingArgs(t *testing.T) {
	next, err := applySettingArgs(nil, []string{"chunk.max_bytes=8MiB", "pool.fill_interval=250ms"}, false)
	require.NoError(t, err)
	require.EqualValues(t, 8<<20, next[skysettings.ChunkMaxBytes])
	require.EqualValues(t, 250_000_000, next[skysettings.PoolFillInterval])

	// A second call folds into what is already set rather than replacing it.
	next, err = applySettingArgs(next, []string{"chunk.concurrency=16"}, false)
	require.NoError(t, err)
	require.Len(t, next, 3)

	// --reset with names drops exactly those.
	next, err = applySettingArgs(next, []string{"chunk.max_bytes"}, true)
	require.NoError(t, err)
	require.NotContains(t, next, skysettings.ChunkMaxBytes)
	require.Contains(t, next, skysettings.ChunkConcurrency)

	// --reset with no names clears everything.
	next, err = applySettingArgs(next, nil, true)
	require.NoError(t, err)
	require.Empty(t, next)

	for _, bad := range [][]string{{"chunk.max_bytes"}, {"nope=1"}, {"pool.fill_interval=250"}} {
		_, err = applySettingArgs(nil, bad, false)
		require.Errorf(t, err, "%v", bad)
	}
	_, err = applySettingArgs(nil, []string{"chunk.max_bytes=1MiB"}, true)
	require.Error(t, err, "--reset takes bare names")
	_, err = applySettingArgs(nil, []string{"nope"}, true)
	require.Error(t, err, "--reset refuses an unknown name")
}

// The table marks a knob default / pending / applied from the two versions —
// the only thing that tells the bench whether the client has the value yet.
func TestSettingsRowsState(t *testing.T) {
	rows := settingsRows("skysocks-client", map[string]int64{skysettings.ChunkMaxBytes: 8 << 20}, 3, 2)
	byName := map[string]string{}
	values := map[string]string{}
	for _, k := range rows.Knobs {
		byName[k.Name] = k.State
		values[k.Name] = k.Value
	}
	require.Equal(t, "pending", byName[skysettings.ChunkMaxBytes])
	require.Equal(t, "8MiB", values[skysettings.ChunkMaxBytes])
	require.Equal(t, "default", byName[skysettings.PoolFillInterval])
	require.Equal(t, "2s", values[skysettings.PoolFillInterval])

	rows = settingsRows("skysocks-client", map[string]int64{skysettings.ChunkMaxBytes: 8 << 20}, 3, 3)
	for _, k := range rows.Knobs {
		if k.Name == skysettings.ChunkMaxBytes {
			require.Equal(t, "applied", k.State)
		}
	}
}
