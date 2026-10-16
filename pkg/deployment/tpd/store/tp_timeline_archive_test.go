package store

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func randomRecords(t *testing.T, n int) []TimelineRecord {
	t.Helper()
	recs := make([]TimelineRecord, n)
	for i := range recs {
		recs[i].ID = uuid.New()
		_, err := rand.Read(recs[i].Bitmap[:])
		require.NoError(t, err)
	}
	return recs
}

// A file spanning several blocks reads back every record, and a lookup
// finds exactly the IDs it holds.
func TestTimelineArchiveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "2026-10-14.tptl")
	recs := randomRecords(t, 3*timelineBlockRecords+17)
	want := make(map[uuid.UUID][transportTimelineBitmapBytes]byte, len(recs))
	for _, r := range recs {
		want[r.ID] = r.Bitmap
	}
	require.NoError(t, WriteTimelineArchive(path, "2026-10-14", recs))

	a, err := OpenTimelineArchive(path)
	require.NoError(t, err)
	defer a.Close() //nolint:errcheck
	require.Equal(t, "2026-10-14", a.Date)
	require.Equal(t, len(recs), a.Records)

	n := 0
	var prev uuid.UUID
	require.NoError(t, a.Each(func(r TimelineRecord) error {
		require.Equal(t, want[r.ID], r.Bitmap)
		require.Negative(t, compareUUID(prev, r.ID), "sorted by ID")
		prev = r.ID
		n++
		return nil
	}))
	require.Equal(t, len(recs), n)

	missing := uuid.New()
	ids := []uuid.UUID{recs[5].ID, recs[2000].ID, recs[len(recs)-1].ID, missing}
	got, err := a.Lookup(ids)
	require.NoError(t, err)
	require.Len(t, got, 3)
	for _, id := range ids[:3] {
		bm := want[id]
		require.Equal(t, bm[:], got[id])
	}

	_, err = OpenTimelineArchive(filepath.Join(t.TempDir(), "none.tptl"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func compareUUID(a, b uuid.UUID) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// A settled day is archived once from redis, and the uptime summaries read it
// back unchanged after its keys have left redis.
func TestArchiveTransportTimelinesReadBack(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	dir := t.TempDir()
	s.SetUptimeArchive(dir)

	now := time.Now().UTC()
	old := now.AddDate(0, 0, -3)
	date := old.Format(MetricsDateFormat)
	today := now.Format(MetricsDateFormat)
	ids := []uuid.UUID{uuid.New(), uuid.New()}
	for i, id := range ids {
		for slot := int64(0); slot < int64(100*(i+1)); slot++ {
			require.NoError(t, s.client.SetBit(ctx, tpUptimeTimelineKey(id.String(), date), slot, 1).Err())
		}
		require.NoError(t, s.client.SetBit(ctx, tpUptimeTimelineKey(id.String(), today), 5, 1).Err())
	}
	before, err := s.GetTransportUptimeSummaries(ctx, ids, true, true)
	require.NoError(t, err)
	require.Equal(t, "34.72", before[0].Daily[date])

	require.NoError(t, s.ArchiveTransportTimelines(ctx, now))
	path := TimelineArchivePath(dir, date)
	st, err := os.Stat(path)
	require.NoError(t, err)
	_, err = os.Stat(TimelineArchivePath(dir, today))
	require.ErrorIs(t, err, os.ErrNotExist, "today has not settled")

	// Idempotent: a second run leaves the file alone.
	require.NoError(t, s.ArchiveTransportTimelines(ctx, now))
	st2, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, st.ModTime(), st2.ModTime())

	for _, id := range ids {
		require.NoError(t, s.client.Del(ctx, tpUptimeTimelineKey(id.String(), date)).Err())
	}
	after, err := s.GetTransportUptimeSummaries(ctx, ids, true, true)
	require.NoError(t, err)
	for i := range ids {
		require.Equal(t, before[i].Daily, after[i].Daily)
		require.Equal(t, before[i].Timeline, after[i].Timeline)
	}
	tl := s.GetTransportDailyTimeline(ctx, ids[1].String(), now)
	require.Equal(t, before[1].Timeline, tl)

	// v2 carries no timelines.
	v2, err := s.GetTransportUptimeSummaries(ctx, ids, true, false)
	require.NoError(t, err)
	require.Nil(t, v2[0].Timeline)
	require.Equal(t, before[0].Daily, v2[0].Daily)
}

// A day settles timelineArchiveAfter after it ends, not before.
func TestArchiveTransportTimelinesWaitsForTheDayToSettle(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	dir := t.TempDir()
	s.SetUptimeArchive(dir)
	day := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	id := uuid.New()
	require.NoError(t, s.client.SetBit(ctx, tpUptimeTimelineKey(id.String(), "2026-10-14"), 1, 1).Err())

	require.NoError(t, s.ArchiveTransportTimelines(ctx, day.Add(24*time.Hour+timelineArchiveAfter-time.Minute)))
	_, err := os.Stat(TimelineArchivePath(dir, "2026-10-14"))
	require.ErrorIs(t, err, os.ErrNotExist)

	require.NoError(t, s.ArchiveTransportTimelines(ctx, day.Add(24*time.Hour+timelineArchiveAfter)))
	a, err := OpenTimelineArchive(TimelineArchivePath(dir, "2026-10-14"))
	require.NoError(t, err)
	defer a.Close() //nolint:errcheck
	require.Equal(t, 1, a.Records)
}

// With the archive the timeline lives 36 h, and a visor's bitmap for an
// archived day no longer brings its key back; without it nothing changes.
func TestTimelineTTLAndArchivedIngest(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	id := uuid.New()
	bm := make([]byte, transportTimelineBitmapBytes)
	bm[0] = 0x80
	old := now.AddDate(0, 0, -3).Format(MetricsDateFormat)
	ttl := func(date string) time.Duration {
		d, err := s.client.TTL(ctx, tpUptimeTimelineKey(id.String(), date)).Result()
		require.NoError(t, err)
		return d
	}

	require.NoError(t, s.IngestTransportTimeline(ctx, id, old, bm))
	require.InDelta(t, tpUptimeTimelineTTL, ttl(old), float64(time.Minute), "no archive: eight days")

	dir := t.TempDir()
	s.SetUptimeArchive(dir)
	require.NoError(t, s.client.FlushDB(ctx).Err())
	require.NoError(t, s.RecordTransportHeartbeat(ctx, id, "stcpr", now))
	require.InDelta(t, tpUptimeTimelineArchivedTTL, ttl(now.Format(MetricsDateFormat)), float64(time.Minute))

	// Not archived yet: the ingest is kept.
	require.NoError(t, s.IngestTransportTimeline(ctx, id, old, bm))
	require.Positive(t, ttl(old))
	require.NoError(t, s.ArchiveTransportTimelines(ctx, now))
	require.NoError(t, s.client.Del(ctx, tpUptimeTimelineKey(id.String(), old)).Err())

	// Archived: the ingest is dropped and the day still reads back.
	require.NoError(t, s.IngestTransportTimeline(ctx, id, old, bm))
	n, err := s.client.Exists(ctx, tpUptimeTimelineKey(id.String(), old)).Result()
	require.NoError(t, err)
	require.Zero(t, n)
	sum, err := s.GetTransportUptimeSummaries(ctx, []uuid.UUID{id}, true, false)
	require.NoError(t, err)
	require.Equal(t, "0.35", sum[0].Daily[old])

	// Yesterday is never dropped.
	yesterday := now.AddDate(0, 0, -1).Format(MetricsDateFormat)
	require.NoError(t, s.IngestTransportTimeline(ctx, id, yesterday, bm))
	require.Positive(t, ttl(yesterday))
}
