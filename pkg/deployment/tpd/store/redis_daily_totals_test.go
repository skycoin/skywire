package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// testRedisEnv names a scratch redis database the tests flush. CI's
// redis-store job sets it, so there the tests must reach redis; unset, they
// skip.
const testRedisEnv = "SKYWIRE_TEST_REDIS"

func newTestRedisStore(t *testing.T) *redisStore {
	t.Helper()
	url := os.Getenv(testRedisEnv)
	if url == "" {
		t.Skipf("%s unset; no redis to test against", testRedisEnv)
	}
	ctx := context.Background()
	s, err := newRedisStore(ctx, url, "", 4, time.Minute, logging.MustGetLogger("test"))
	require.NoError(t, err)
	require.NoError(t, s.client.FlushDB(ctx).Err())
	t.Cleanup(func() { _ = s.client.FlushDB(ctx).Err() }) //nolint:errcheck
	return s
}

func netDaily(t *testing.T, s *redisStore) map[string]string {
	t.Helper()
	h, err := s.client.HGetAll(context.Background(), s.netDailyKey(time.Now().UTC().Format(MetricsDateFormat))).Result()
	require.NoError(t, err)
	return h
}

// A transport counts as the max over its reporters of sent+recv, and the
// network total is the sum of those maxima, kept as reports arrive.
func TestDailyTotalsBandwidthIsSumOfTransportMaxima(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	tp1, tp2 := uuid.New().String(), uuid.New().String()

	require.NoError(t, s.UpdateBandwidth(ctx, tp1, a, 100, 50, "stcpr")) // tp1 A=150
	require.NoError(t, s.UpdateBandwidth(ctx, tp1, b, 120, 20, "stcpr")) // tp1 B=140, max 150
	require.NoError(t, s.UpdateBandwidth(ctx, tp2, a, 10, 0, "dmsg"))    // tp2 A=10
	require.Equal(t, "160", netDaily(t, s)["bandwidth"])
	require.Equal(t, "150", netDaily(t, s)["type:stcpr"])
	require.Equal(t, "10", netDaily(t, s)["type:dmsg"])

	require.NoError(t, s.UpdateBandwidth(ctx, tp1, b, 180, 30, "stcpr")) // B=210 > 150
	require.Equal(t, "220", netDaily(t, s)["bandwidth"])

	// A counter reset counts from zero: A's 5+5 is new traffic, and A's
	// total 160 is still under tp1's max of 210.
	require.NoError(t, s.UpdateBandwidth(ctx, tp1, a, 5, 5, "stcpr"))
	require.Equal(t, "220", netDaily(t, s)["bandwidth"])
	h, err := s.client.HGetAll(ctx, s.bandwidthDailyKey(tp1, time.Now().UTC())).Result()
	require.NoError(t, err)
	require.Equal(t, "210", h["max"])
	require.Equal(t, "105", h[a.Hex()+":sent"])
}

// Latency is the mean over the day's transports of each one's latest.
func TestDailyTotalsLatencyIsMeanOfLatest(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	tp1, tp2 := uuid.New().String(), uuid.New().String()

	require.NoError(t, s.UpdateLatency(ctx, tp1, 5, 15, 10, "stcpr"))
	require.NoError(t, s.UpdateLatency(ctx, tp2, 20, 40, 30, "stcpr"))
	require.NoError(t, s.UpdateLatency(ctx, tp1, 10, 30, 20, "stcpr")) // replaces 10

	day := time.Now().UTC().Format(MetricsDateFormat)
	agg, ok := dailyFromTotals(day, netDaily(t, s), MetricsQuery{Latency: true})
	require.True(t, ok)
	require.InDelta(t, 25, agg.Latency, 1e-9)
	require.InDelta(t, 25, agg.ByType["stcpr"].Latency, 1e-9)
}

// Days after the totals began are answered from them; the rest scan.
func TestGetNetworkMetricsReadsCompleteDaysFromTotals(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	a, _ := cipher.GenerateKeyPair()
	require.NoError(t, s.UpdateBandwidth(ctx, uuid.New().String(), a, 300, 200, "sudph"))

	// The first report marks today as the start, so today is not complete.
	day := time.Now().UTC().Format(MetricsDateFormat)
	totals, err := s.dailyTotals(ctx, []string{day}, MetricsQuery{Bandwidth: true})
	require.NoError(t, err)
	require.Empty(t, totals)

	// Pretend the totals began earlier: today is complete and read from them.
	require.NoError(t, s.client.Set(ctx, s.netDailySinceKey(), "2000-01-01", 0).Err())
	resp, err := s.GetNetworkMetrics(ctx, MetricsQuery{Days: 2, Bandwidth: true, Latency: true})
	require.NoError(t, err)
	require.Len(t, resp.Daily, 1)
	require.Equal(t, day, resp.Daily[0].Date)
	require.EqualValues(t, 500, resp.Daily[0].Bandwidth)
	require.EqualValues(t, 500, resp.Cumulative.Bandwidth)
	require.EqualValues(t, 500, resp.Cumulative.ByType["sudph"].Bandwidth)
}
