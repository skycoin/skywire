// Package skysocks pkg/skysocks/settings_defaults_test.go
package skysocks

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// Every knob's compiled default must equal the constant the use site used to
// read. This is the whole safety property of the settings surface: a client
// with nothing set behaves byte for byte as it did before the registry
// existed, so a bench run that sets nothing is still a clean baseline.
//
// The registry lives in its own package (the CLI reads the catalog without
// linking pkg/skysocks), which is exactly why the two halves can drift — this
// test is the only thing holding them together.
func TestSettingDefaultsMatchConstants(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	require.Equal(t, 2*1e9, float64(setPoolFillInterval()), "pool.fill_interval")
	require.Equal(t, poolRetryBackoffBase, setPoolRetryBackoffBase())
	require.Equal(t, poolRetryBackoffMax, setPoolRetryBackoffMax())
	require.Equal(t, poolRetryRounds, setPoolRetryRounds())
	require.Equal(t, standbyRTTStale, setStandbyRTTStale())

	require.Equal(t, tunnelRTTProbeInterval, setTunnelRTTProbeInterval())
	require.Equal(t, livenessProbeInterval, setLivenessProbeInterval())
	require.Equal(t, tunnelRTTAlpha, setTunnelRTTAlpha())
	require.Equal(t, tunnelPromoteInterval, setTunnelPromoteInterval())
	require.Equal(t, tunnelPromoteMargin, setTunnelPromoteMargin())
	require.Equal(t, tunnelPromoteHold, setTunnelPromoteHold())
	require.Equal(t, tunnelParkMinHold, setTunnelParkMinHold())
	require.Equal(t, tunnelAuditionWindow, setTunnelAuditionWindow())
	require.Equal(t, tunnelAuditionEvery, setTunnelAuditionEvery())
	require.Equal(t, exitOpenPenalty, setExitOpenPenalty())
	require.Equal(t, meterSampleMin, setMeterSampleMin())
	require.Equal(t, meterCapDecay, setMeterCapDecay())
	require.Equal(t, meterFresh, setMeterFresh())

	require.Equal(t, rsChunkRetryBudget, setChunkRetryBudget())
	require.Equal(t, rsChunkIdleTimeout, setChunkIdleTimeout())
	require.Equal(t, rsFreeRetries, setChunkFreeRetries())
	require.Equal(t, rsOutstandingFactor, setChunkOutstandingFactor())

	require.EqualValues(t, defaultRSChunkSize, skysettings.Bytes(skysettings.ChunkMaxBytes))
	require.Equal(t, defaultRSConcurrency, skysettings.Count(skysettings.ChunkConcurrency))
	require.Equal(t, poolFillInterval, setPoolFillInterval())
}

// The two range-split knobs OVERRIDE a per-client boot flag rather than
// replacing it: unset, the flag wins; set, the knob does. Anything else would
// make --range-chunk-kib silently inert.
func TestChunkKnobsOverrideTheBootFlags(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c := &Client{rs: rangeSplitConfig{chunkSize: 1 << 20, concurrency: 3}}
	require.EqualValues(t, 1<<20, c.rsChunkSize(), "unset, the flag wins")
	require.Equal(t, 3, c.rsConcurrency())

	require.True(t, skysettings.Apply(map[string]int64{
		skysettings.ChunkMaxBytes:    8 << 20,
		skysettings.ChunkConcurrency: 16,
	}))
	require.EqualValues(t, 8<<20, c.rsChunkSize(), "set, the knob wins")
	require.Equal(t, 16, c.rsConcurrency())

	require.True(t, skysettings.Reset())
	require.EqualValues(t, 1<<20, c.rsChunkSize(), "reset returns the flag")
}

// pullSettings installs what the visor answers and reports whether anything
// moved, which is what re-cadences the keepalive tickers.
func TestPullSettingsAppliesAndVersions(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	version := uint64(0)
	vals := map[string]int64{}
	c := &Client{appSettings: func(applied uint64) (map[string]int64, uint64, error) {
		if applied == version {
			return nil, version, nil
		}
		return vals, version, nil
	}}

	require.False(t, c.pullSettings(), "nothing set, nothing to do")

	vals, version = map[string]int64{skysettings.TunnelAuditionEvery: int64(20e9)}, 1
	require.True(t, c.pullSettings())
	require.EqualValues(t, uint64(1), c.settingsApplied)
	require.EqualValues(t, 20e9, setTunnelAuditionEvery())

	require.False(t, c.pullSettings(), "the version is unchanged, so the pull is a no-op")
}
