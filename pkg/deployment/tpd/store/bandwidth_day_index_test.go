// Package store pkg/deployment/tpd/store/bandwidth_day_index_test.go c4-net-discovery
package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestParseBWDailyKey(t *testing.T) {
	prefix := serviceName + ":bw:daily:"
	id := uuid.New()
	gotID, day, ok := parseBWDailyKey(prefix+id.String()+":2026-09-10", prefix)
	require.True(t, ok)
	require.Equal(t, id, gotID)
	require.Equal(t, "2026-09-10", day.Format("2006-01-02"))

	for _, bad := range []string{prefix + "nope", prefix + id.String() + ":yesterday", prefix + "x:2026-09-10"} {
		_, _, ok := parseBWDailyKey(bad, prefix)
		require.False(t, ok, bad)
	}
}

func testIndex(anchor time.Time) *bwDayIndex {
	return &bwDayIndex{
		anchor:    epochDay(anchor),
		byID:      map[uuid.UUID]uint64{},
		edges:     map[uuid.UUID][2]cipher.PubKey{},
		scannedAt: anchor,
	}
}

// A nil index knows nothing: every day is fetched, as before the index existed.
func TestBWDayIndex_NilFetchesEverything(t *testing.T) {
	var ix *bwDayIndex
	require.Equal(t, []int{0, 1, 2, 3, 4}, ix.fetchDays(uuid.New(), time.Now(), 5))
}

// Only days the scan saw are fetched, plus today and yesterday, which may
// have gained a hash since the scan. Unknown transports get just those two.
func TestBWDayIndex_FetchDays(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	ix := testIndex(now)
	known := uuid.New()
	ix.byID[known] = 1<<5 | 1<<12 | 1<<29 // data 5, 12 and 29 days ago
	require.Equal(t, []int{0, 1, 5, 12, 29}, ix.fetchDays(known, now, 30))
	require.Equal(t, []int{0, 1, 5, 12}, ix.fetchDays(known, now, 20))
	require.Equal(t, []int{0}, ix.fetchDays(known, now, 1))
	require.Equal(t, []int{0, 1}, ix.fetchDays(uuid.New(), now, 30))
}

// After a UTC rollover the scan's "today" is the caller's "yesterday": bits
// must shift with the calendar, not with the offset.
func TestBWDayIndex_Rollover(t *testing.T) {
	scanned := time.Date(2026, 9, 10, 23, 58, 0, 0, time.UTC)
	ix := testIndex(scanned)
	id := uuid.New()
	ix.byID[id] = 1 << 3 // data on 2026-09-07
	later := time.Date(2026, 9, 11, 0, 2, 0, 0, time.UTC)
	require.Equal(t, []int{0, 1, 4}, ix.fetchDays(id, later, 30), "09-07 is now 4 days back")

	// The window mask likewise follows the calendar: a 3-day window from the
	// 11th covers the 9th..11th and must not match data on the 7th.
	require.Zero(t, ix.byID[id]&ix.windowMask(later, 3))
	require.NotZero(t, ix.byID[id]&ix.windowMask(later, 5))
}

func TestBWDayIndex_DayBitBounds(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	ix := testIndex(now)
	_, ok := ix.dayBit(now.AddDate(0, 0, 1))
	require.False(t, ok, "a day after the scan has no bit")
	_, ok = ix.dayBit(now.AddDate(0, 0, -64))
	require.False(t, ok, "beyond the mask width has no bit")
	k, ok := ix.dayBit(now.AddDate(0, 0, -63))
	require.True(t, ok)
	require.Equal(t, uint(63), k)
}

func TestNewestBit(t *testing.T) {
	require.Equal(t, 0, newestBit(0))
	require.Equal(t, 0, newestBit(1))
	require.Equal(t, 3, newestBit(1<<3|1<<9))
}

func TestEdgesFromDailyHash(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	edges, ok := edgesFromDailyHash(map[string]string{a.Hex() + ":sent": "1", a.Hex() + ":recv": "2", b.Hex() + ":sent": "3"})
	require.True(t, ok)
	require.ElementsMatch(t, []cipher.PubKey{a, b}, edges[:])

	_, ok = edgesFromDailyHash(map[string]string{"bandwidth": "10"})
	require.False(t, ok, "legacy combined hash has no edges")
}
