package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A saved leaf lands on disk and in redis for leafLiveDays. Older leaves leave
// redis only once archived, and a load falls back to the archive.
func TestLeafArchive(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	s.SetLeafArchive(t.TempDir())
	now := time.Now().UTC()
	day := func(back int) string { return now.AddDate(0, 0, -back).Format(MetricsDateFormat) }
	parts := [][]byte{[]byte("part one"), {}, []byte("part three")}

	require.NoError(t, s.SaveMetricsLeaf(ctx, day(2), parts))
	ttl, err := s.client.TTL(ctx, s.metricsLeafKey(day(2))).Result()
	require.NoError(t, err)
	require.LessOrEqual(t, ttl, leafLiveDays*24*time.Hour)
	onDisk, err := s.readLeafFile(day(2))
	require.NoError(t, err)
	require.Equal(t, parts, onDisk)

	// Leaves saved before the archive existed: one old, one recent.
	for _, d := range []string{day(20), day(3)} {
		require.NoError(t, s.client.RPush(ctx, s.metricsLeafKey(d), "a", "b").Err())
	}
	require.NoError(t, s.archiveLeaves(ctx, now))
	n, err := s.client.Exists(ctx, s.metricsLeafKey(day(20))).Result()
	require.NoError(t, err)
	require.Zero(t, n, "an archived leaf past leafLiveDays leaves redis")
	n, err = s.client.Exists(ctx, s.metricsLeafKey(day(3))).Result()
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "a recent leaf stays in redis")
	_, err = os.Stat(s.leafFile(day(3)))
	require.NoError(t, err, "a recent leaf is archived too")

	got, err := s.LoadMetricsLeaves(ctx, []string{day(20), day(3), day(40)})
	require.NoError(t, err)
	require.Equal(t, [][]byte{[]byte("a"), []byte("b")}, got[day(20)], "an old day is read from the archive")
	require.Contains(t, got, day(3))
	require.NotContains(t, got, day(40))
}
