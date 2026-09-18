// Package skysettings pkg/skysocks/skysettings/skysettings_test.go
package skysettings

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Format then Parse is the identity on every knob, for its default and for a
// value the bench would plausibly type. Without this a `--json` table could
// print a value the same command refuses to take back.
func TestParseFormatRoundTrip(t *testing.T) {
	t.Cleanup(func() { Reset() })
	for _, d := range Catalog() {
		back, err := Parse(d.Name, Format(d.Name, d.Default))
		require.NoErrorf(t, err, "%s: %s", d.Name, Format(d.Name, d.Default))
		require.Equalf(t, d.Default, back, "%s round-trips its default", d.Name)
	}
}

func TestParseUnits(t *testing.T) {
	t.Cleanup(func() { Reset() })
	v, err := Parse(ChunkMaxBytes, "8MiB")
	require.NoError(t, err)
	require.EqualValues(t, 8<<20, v)

	v, err = Parse(ChunkMaxBytes, "2097152")
	require.NoError(t, err)
	require.EqualValues(t, 2<<20, v)

	v, err = Parse(PoolFillInterval, "250ms")
	require.NoError(t, err)
	require.EqualValues(t, 250_000_000, v)

	v, err = Parse(TunnelPromoteMargin, "1.1")
	require.NoError(t, err)
	require.True(t, Apply(map[string]int64{TunnelPromoteMargin: v}))
	require.InDelta(t, 1.1, Ratio(TunnelPromoteMargin), 1e-9)

	_, err = Parse(PoolFillInterval, "250")
	require.Error(t, err, "a duration without a unit is refused")
	_, err = Parse(ChunkConcurrency, "0")
	require.Error(t, err, "a non-positive count is refused")
	_, err = Parse("nope.not.a.knob", "1")
	require.Error(t, err)
}

// Apply is WHOLESALE: a knob the map does not name goes back to its compiled
// default. That is what makes a reset on the visor side a plain delete.
func TestApplyIsWholesaleAndBumpsVersion(t *testing.T) {
	t.Cleanup(func() { Reset() })
	require.False(t, IsSet(ChunkMaxBytes))
	before := Version()

	require.True(t, Apply(map[string]int64{ChunkMaxBytes: 8 << 20}))
	require.EqualValues(t, 8<<20, Bytes(ChunkMaxBytes))
	require.True(t, IsSet(ChunkMaxBytes))
	require.Greater(t, Version(), before)

	// Re-applying the same map changes nothing.
	v := Version()
	require.False(t, Apply(map[string]int64{ChunkMaxBytes: 8 << 20}))
	require.Equal(t, v, Version())

	// A map that no longer names it is a reset of it.
	require.True(t, Apply(map[string]int64{PoolFillInterval: int64(3e9)}))
	require.EqualValues(t, 4<<20, Bytes(ChunkMaxBytes))
	require.False(t, IsSet(ChunkMaxBytes))
	require.True(t, IsSet(PoolFillInterval))

	require.True(t, Reset())
	require.False(t, IsSet(PoolFillInterval))
}

// An unknown name in the map is ignored rather than fatal: an older app must be
// able to pull a newer visor's set.
func TestApplyIgnoresUnknown(t *testing.T) {
	t.Cleanup(func() { Reset() })
	require.True(t, Apply(map[string]int64{"future.knob": 1, ChunkConcurrency: 16}))
	require.Equal(t, 16, Count(ChunkConcurrency))
}
