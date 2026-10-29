// Package uptimestats pkg/uptimestats/uptimestats_test.go
package uptimestats

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// --- filter.go: pure helpers ---

func sv(pk cipher.PubKey, online bool, version string) VisorSummary {
	return VisorSummary{PK: pk, Online: online, Version: version}
}

func TestFilter(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	entries := []VisorSummary{
		sv(pk1, true, "v1.3.0"),
		sv(pk2, false, "v1.2.0"),
	}

	t.Run("zero value matches all", func(t *testing.T) {
		assert.Len(t, Filter(entries, VisorFilter{}), 2)
	})

	t.Run("online only", func(t *testing.T) {
		got := Filter(entries, VisorFilter{OnlineOnly: true})
		require.Len(t, got, 1)
		assert.Equal(t, pk1, got[0].PK)
	})

	t.Run("pk substring", func(t *testing.T) {
		sub := pk2.Hex()[:8]
		got := Filter(entries, VisorFilter{PKSubstr: sub})
		require.Len(t, got, 1)
		assert.Equal(t, pk2, got[0].PK)

		assert.Empty(t, Filter(entries, VisorFilter{PKSubstr: "zzzzzzzz"}))
	})

	t.Run("version equals", func(t *testing.T) {
		got := Filter(entries, VisorFilter{VersionEquals: "1.3.0"})
		require.Len(t, got, 1)
		assert.Equal(t, "v1.3.0", got[0].Version)
	})

	t.Run("min version", func(t *testing.T) {
		got := Filter(entries, VisorFilter{MinVersion: "1.3.0"})
		require.Len(t, got, 1)
		assert.Equal(t, "v1.3.0", got[0].Version)
	})

	t.Run("does not mutate input", func(t *testing.T) {
		_ = Filter(entries, VisorFilter{OnlineOnly: true})
		assert.Len(t, entries, 2)
	})
}

func TestVersionCounts(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	entries := []VisorSummary{
		sv(pk, true, "v1.3.0"),
		sv(pk, true, "v1.3.0"),
		sv(pk, true, "v1.2.0"),
		sv(pk, true, "v1.4.0"),
	}
	got := VersionCounts(entries)
	require.Len(t, got, 3)
	// Highest count first.
	assert.Equal(t, VersionCount{Version: "v1.3.0", Count: 2}, got[0])
	// Ties broken by version ascending.
	assert.Equal(t, "v1.2.0", got[1].Version)
	assert.Equal(t, "v1.4.0", got[2].Version)
}

func TestMinDailyUptime(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	good := VisorSummary{PK: pk, Daily: map[string]string{"2026-06-18": "99", "2026-06-19": "80"}}
	low := VisorSummary{PK: pk, Daily: map[string]string{"2026-06-18": "50"}}
	noDaily := VisorSummary{PK: pk}
	bad := VisorSummary{PK: pk, Daily: map[string]string{"2026-06-18": "not-a-number"}}

	got := MinDailyUptime([]VisorSummary{good, low, noDaily, bad}, 75)
	require.Len(t, got, 1)
	assert.Equal(t, good.Daily, got[0].Daily)
}

func TestNormalizeVersion(t *testing.T) {
	assert.Equal(t, "1.3.0", normalizeVersion("v1.3.0"))
	assert.Equal(t, "1.3.0", normalizeVersion("1.3.0 (abc)"))
	assert.Equal(t, "", normalizeVersion(""))
}

func TestParseSemver(t *testing.T) {
	maj, min, patch, ok := parseSemver("v1.2.3-0")
	assert.True(t, ok)
	assert.Equal(t, 1, maj)
	assert.Equal(t, 2, min)
	assert.Equal(t, 3, patch)

	_, _, _, ok = parseSemver("")
	assert.False(t, ok)
	_, _, _, ok = parseSemver("1.2")
	assert.False(t, ok)
	_, _, _, ok = parseSemver("x.2.3")
	assert.False(t, ok)
	_, _, _, ok = parseSemver("1.y.3")
	assert.False(t, ok)
}

func TestVersionGTE(t *testing.T) {
	assert.True(t, versionGTE("1.3.0", "1.2.0"))  // minor greater
	assert.True(t, versionGTE("2.0.0", "1.9.9"))  // major greater
	assert.True(t, versionGTE("1.2.3", "1.2.3"))  // equal
	assert.False(t, versionGTE("1.2.0", "1.3.0")) // less
	assert.False(t, versionGTE("bad", "1.0.0"))   // unparsable
}
