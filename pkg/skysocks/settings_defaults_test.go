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

	require.EqualValues(t, rsProbeChunkBytes, setChunkProbeBytes())
	require.EqualValues(t, rsMinChunkBytes, setChunkMinBytes())
	require.Equal(t, rsChunksPerTunnel, setChunkPerTunnel())

	require.EqualValues(t, defaultRSChunkSize, skysettings.Bytes(skysettings.ChunkMaxBytes))
	require.Equal(t, defaultRSConcurrency, skysettings.Count(skysettings.ChunkConcurrency))

	// The striped upload. The four the tests shrink read the package var while
	// the knob is unset, so with nothing set the two are the same number.
	require.Equal(t, uploadStripeMinBytes, setUploadStripeMinBytes())
	require.Equal(t, uploadChunkBytes, setUploadChunkBytes())
	require.Equal(t, uploadMemBytes, setUploadMemBytes())
	require.Equal(t, uploadConcurrency, setUploadConcurrency())
	require.Equal(t, uploadReplayMaxBytes, setUploadReplayMaxBytes())
	require.Equal(t, uploadProbeTTL, setUploadProbeTTL())
	require.Equal(t, uploadAckTimeout, setUploadAckTimeout())
	require.Equal(t, uploadIdleTimeout, setUploadIdleTimeout())
	require.Equal(t, uploadDurableWait, setUploadDurableWait())
	require.Equal(t, uploadResendPasses, setUploadResendPasses())
	require.Equal(t, uploadEarlyTries, setUploadEarlyTries())
	require.Equal(t, uploadEarlyWaitMax, setUploadEarlyWaitMax())
	require.Equal(t, uploadBusyBackoff, setUploadBusyBackoff())
	require.Equal(t, uploadBusyTries, setUploadBusyTries())
	require.Equal(t, uploadCutTries, setUploadCutTries())
	require.Equal(t, uploadReplayTries, setUploadReplayTries())

	// The spread policy defaults to OFF in all four of its knobs — the property
	// criterion 10 rests on, since every measurement before it was taken with
	// no spread policy at all.
	require.Equal(t, 1.0, setSpreadMaxShare(), "spread.max_share")
	require.Equal(t, 0, setSpreadMinRoutes(), "spread.min_routes")
	require.False(t, setSpreadEndgame(), "spread.endgame")
	require.Equal(t, skysettings.SpreadWeightRate, setSpreadWeight(), "spread.weight")
	require.False(t, spreadPolicyNow().steers(), "with nothing set the planner steers nothing")

	// The snub and the bandwidth-delay depth. Both depth_dynamic flags are OFF
	// by default, which is the whole no-flag-gate contract here: the mechanism
	// ships inert and the operator turns it on live.
	require.Equal(t, tunnelSnubAfter, setTunnelSnubAfter())
	require.Equal(t, tunnelSnubHold, setTunnelSnubHold())
	require.Equal(t, tunnelDepthMargin, setTunnelDepthMargin())
	require.Equal(t, rsDepthMin, setChunkDepthMin())
	require.Equal(t, rsDepthMax, setChunkDepthMax())
	require.False(t, setChunkDepthDynamic(), "chunk.depth_dynamic is off until the operator says otherwise")
	require.False(t, setUploadDepthDynamic(), "upload.depth_dynamic is off until the operator says otherwise")

	// The ONE default that is deliberately ON: the burst placement, whose
	// predecessor is the measured defect and not a baseline.
	require.True(t, setUploadBurstPlan(), "upload.burst_plan places a one-burst object by capacity")

	// Every knob in the catalog is reachable: a name registered with no use site
	// reading it is a knob the bench can set and nothing obeys.
	require.Len(t, skysettings.Catalog(), 56)
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

// The four upload knobs the tests shrink OVERRIDE the package var rather than
// replacing it, and slots()/headroom() take one snapshot per call: with the
// knob set, both halves of the window arithmetic move together.
func TestUploadKnobsOverrideAndAreSnapshotted(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	defer restoreUploadTunables(uploadChunkBytes, uploadMemBytes, uploadStripeMinBytes, uploadConcurrency)()
	uploadChunkBytes, uploadMemBytes, uploadConcurrency = 4<<20, 32<<20, 4

	s := &uploadStripe{u: &uploadCandidate{window: 32 << 20}}
	require.Equal(t, 4, s.perTunnel(), "unset, the package var wins")
	require.Equal(t, 4, s.slots(), "8 of our own, less the 4-chunk headroom")

	require.True(t, skysettings.Apply(map[string]int64{
		skysettings.UploadChunkBytes:  8 << 20,
		skysettings.UploadConcurrency: 2,
	}))
	// 32 MiB of memory over 8 MiB chunks is 4; the sink's 32 MiB window is 4
	// chunks less a 2-chunk headroom. Both halves read the NEW chunk size.
	require.Equal(t, 2, s.perTunnel(), "set, the knob wins")
	require.Equal(t, 2, s.headroom())
	require.Equal(t, 2, s.slots())

	require.True(t, skysettings.Reset())
	require.Equal(t, 4, s.perTunnel(), "reset returns the package var")
	require.Equal(t, 4, s.slots())
}
