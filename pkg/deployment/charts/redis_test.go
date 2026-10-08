package charts

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// SKYWIRE_TEST_REDIS names a scratch redis database. CI's redis-store job
// sets it; unset, the test skips.
func TestRedisStore(t *testing.T) {
	url := os.Getenv("SKYWIRE_TEST_REDIS")
	if url == "" {
		t.Skip("SKYWIRE_TEST_REDIS unset; no redis to test against")
	}
	ctx := context.Background()
	st, err := NewRedisStore(url, "", "charts-test")
	require.NoError(t, err)
	b := st.(*store).b.(*redisBackend)
	clean := func() {
		require.NoError(t, b.c.Del(ctx, b.key(false), b.key(true), b.prefix+":rolled", b.startsKey()).Err())
	}
	clean()
	t.Cleanup(clean)

	base := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 24; i++ {
		require.NoError(t, st.Add(ctx, Sample{At: base.Add(time.Duration(i) * Interval), V: map[string]float64{"a": float64(i)}}))
	}
	// A repeated timestamp replaces the sample rather than adding a second.
	require.NoError(t, st.Add(ctx, Sample{At: base.Add(23 * Interval), V: map[string]float64{"a": 100}}))

	raw, err := st.Range(ctx, base, base.Add(3*time.Hour), false)
	require.NoError(t, err)
	require.Len(t, raw, 24)
	require.Equal(t, 100.0, raw[23].V["a"])

	hourly, err := st.Range(ctx, base, base.Add(3*time.Hour), true)
	require.NoError(t, err)
	require.Len(t, hourly, 1)
	require.Equal(t, base, hourly[0].At)
	require.InDelta(t, 5.5, hourly[0].V["a"], 1e-9)

	require.NoError(t, st.AddStart(ctx, Start{At: base, Version: "v1", Commit: "c1"}))
	starts, err := st.Starts(ctx, base, base.Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, []Start{{At: base, Version: "v1", Commit: "c1"}}, starts)
}
