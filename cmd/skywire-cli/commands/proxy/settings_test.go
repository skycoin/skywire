// Package skysocksc cmd/skywire-cli/commands/proxy/settings_test.go
package skysocksc

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

func TestApplySettingArgs(t *testing.T) {
	next, _, err := applySettingArgs(nil, nil, []string{"chunk.max_bytes=8MiB", "pool.fill_interval=250ms"}, false)
	require.NoError(t, err)
	require.EqualValues(t, 8<<20, next[skysettings.ChunkMaxBytes])
	require.EqualValues(t, 250_000_000, next[skysettings.PoolFillInterval])

	// A second call folds into what is already set rather than replacing it.
	next, _, err = applySettingArgs(next, nil, []string{"chunk.concurrency=16"}, false)
	require.NoError(t, err)
	require.Len(t, next, 3)

	// --reset with names drops exactly those.
	next, _, err = applySettingArgs(next, nil, []string{"chunk.max_bytes"}, true)
	require.NoError(t, err)
	require.NotContains(t, next, skysettings.ChunkMaxBytes)
	require.Contains(t, next, skysettings.ChunkConcurrency)

	// --reset with no names clears everything.
	next, _, err = applySettingArgs(next, nil, nil, true)
	require.NoError(t, err)
	require.Empty(t, next)

	for _, bad := range [][]string{{"chunk.max_bytes"}, {"nope=1"}, {"pool.fill_interval=250"}} {
		_, _, err = applySettingArgs(nil, nil, bad, false)
		require.Errorf(t, err, "%v", bad)
	}
	_, _, err = applySettingArgs(nil, nil, []string{"chunk.max_bytes=1MiB"}, true)
	require.Error(t, err, "--reset takes bare names")
	_, _, err = applySettingArgs(nil, nil, []string{"nope"}, true)
	require.Error(t, err, "--reset refuses an unknown name")
}

// The table marks a knob default / pending / applied from the two versions —
// the only thing that tells the bench whether the client has the value yet.
func TestSettingsRowsState(t *testing.T) {
	rows := settingsRows("skysocks-client", map[string]int64{skysettings.ChunkMaxBytes: 8 << 20}, nil, 3, 2)
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

	rows = settingsRows("skysocks-client", map[string]int64{skysettings.ChunkMaxBytes: 8 << 20}, nil, 3, 3)
	for _, k := range rows.Knobs {
		if k.Name == skysettings.ChunkMaxBytes {
			require.Equal(t, "applied", k.State)
		}
	}
}

// A LIST knob is parsed into the text map, validated on the way in, and
// rendered from what the visor holds rather than from this process's own
// registry — the CLI never applies a setting to itself.
func TestApplySettingArgsListKnobs(t *testing.T) {
	const pk = "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"

	vals, text, err := applySettingArgs(nil, nil, []string{
		"pool.exclude_pks=" + pk,
		"pool.require_tp_types=stcpr, sudph",
		"chunk.max_bytes=8MiB",
	}, false)
	require.NoError(t, err)
	require.Equal(t, pk, text[skysettings.PoolExcludePKs])
	require.Equal(t, "stcpr,sudph", text[skysettings.PoolRequireTpTypes])
	require.EqualValues(t, 8<<20, vals[skysettings.ChunkMaxBytes])

	// An empty value IS the default, so it drops the entry rather than
	// carrying an empty string to the app.
	_, text, err = applySettingArgs(nil, text, []string{"pool.exclude_pks="}, false)
	require.NoError(t, err)
	require.NotContains(t, text, skysettings.PoolExcludePKs)

	// --reset by name drops a list knob too.
	_, text, err = applySettingArgs(nil, text, []string{"pool.require_tp_types"}, true)
	require.NoError(t, err)
	require.Empty(t, text)

	// A truncated key is refused: it would exclude nothing at all.
	_, _, err = applySettingArgs(nil, nil, []string{"pool.exclude_pks=0281a102"}, false)
	require.Error(t, err)

	// The rendered row reads the visor's text map.
	rows := settingsRows("skysocks-client", nil, map[string]string{skysettings.PoolExcludePKs: pk}, 2, 2)
	for _, k := range rows.Knobs {
		switch k.Name {
		case skysettings.PoolExcludePKs:
			require.Equal(t, pk, k.Value)
			require.Equal(t, "applied", k.State)
		case skysettings.PoolRequireTpTypes:
			require.Empty(t, k.Value)
			require.Equal(t, "default", k.State)
		}
	}
}
