// Package skysettings pkg/skysocks/skysettings/skysettings_test.go
package skysettings

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Format then Parse is the identity on every knob, for its default and for a
// value the bench would plausibly type. Without this a `--json` table could
// print a value the same command refuses to take back.
func TestParseFormatRoundTrip(t *testing.T) {
	t.Cleanup(func() { Reset() })
	for _, d := range Catalog() {
		if d.Kind == KindList {
			// A list knob's payload is a string, not an int64; its round trip
			// is TestListRoundTrip below.
			continue
		}
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

// A list knob installs, reads back and resets like every other knob — and
// refuses a truncated public key, which would otherwise silently exclude
// nothing.
func TestListRoundTrip(t *testing.T) {
	t.Cleanup(func() { Reset() })
	const pk = "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"

	require.Empty(t, Strings(PoolExcludePKs))
	require.False(t, IsSet(PoolExcludePKs))
	require.True(t, IsList(PoolExcludePKs))

	norm, err := ParseList(PoolExcludePKs, " "+strings.ToUpper(pk)+" , ")
	require.NoError(t, err)
	require.Equal(t, pk, norm)

	require.True(t, ApplyText(map[string]string{PoolExcludePKs: norm}))
	require.Equal(t, []string{pk}, Strings(PoolExcludePKs))
	require.True(t, IsSet(PoolExcludePKs))
	require.Equal(t, pk, Format(PoolExcludePKs, 0))

	// A second identical apply is not a change; dropping the key is.
	require.False(t, ApplyText(map[string]string{PoolExcludePKs: norm}))
	require.True(t, ApplyText(nil))
	require.Empty(t, Strings(PoolExcludePKs))
	require.False(t, IsSet(PoolExcludePKs))

	_, err = ParseList(PoolExcludePKs, "0281a102")
	require.Error(t, err)
	_, err = ParseList(PoolRequireTpTypes, "stcpr, sudph")
	require.NoError(t, err)
}

// The numeric half of a pull must not clear the list half's IsSet, nor the
// other way round: they arrive as two maps in one answer.
func TestApplyAndApplyTextAreIndependent(t *testing.T) {
	t.Cleanup(func() { Reset() })
	require.True(t, ApplyText(map[string]string{PoolRequireTpTypes: "stcpr"}))
	require.True(t, Apply(map[string]int64{PoolSize: 4}))
	require.Equal(t, []string{"stcpr"}, Strings(PoolRequireTpTypes))
	require.True(t, IsSet(PoolRequireTpTypes))
	require.Equal(t, 4, Count(PoolSize))
}
