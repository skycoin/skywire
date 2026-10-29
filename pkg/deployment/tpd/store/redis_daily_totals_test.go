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

func newTestRedisStore(t testing.TB) *redisStore {
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

// seedPrev stores a zero snapshot for reporter's counters on id, as a live
// transport's first report leaves, so the test's reports are deltas from it.
func seedPrev(t *testing.T, s *redisStore, id string, reporter cipher.PubKey) {
	t.Helper()
	require.NoError(t, s.client.HSet(context.Background(), s.bandwidthPrevKey(id, reporter.Hex()), "sent", 0, "recv", 0).Err())
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
	seedPrev(t, s, tp1, a)
	seedPrev(t, s, tp1, b)
	seedPrev(t, s, tp2, a)

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
	id := uuid.New().String()
	seedPrev(t, s, id, a)
	require.NoError(t, s.UpdateBandwidth(ctx, id, a, 300, 200, "sudph"))

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

// A reporter's last counters outlive a gap in its reports, so a later report
// counts only what moved and not the transport's whole lifetime again.
func TestBandwidthPrevOutlivesRestart(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	a, _ := cipher.GenerateKeyPair()
	id := uuid.New().String()
	require.NoError(t, s.UpdateBandwidth(ctx, id, a, 1000, 500, "stcpr"))
	ttl, err := s.client.TTL(ctx, s.bandwidthPrevKey(id, a.Hex())).Result()
	require.NoError(t, err)
	require.InDelta(t, bandwidthPrevTTL, ttl, float64(time.Minute))

	// Six hours on, the old six hour TTL would have dropped it.
	require.NoError(t, s.client.Expire(ctx, s.bandwidthPrevKey(id, a.Hex()), bandwidthPrevTTL-6*time.Hour).Err())
	require.NoError(t, s.UpdateBandwidth(ctx, id, a, 1100, 600, "stcpr"))
	sent, err := s.client.HGet(ctx, s.bandwidthDailyKey(id, time.Now().UTC()), a.Hex()+":sent").Int64()
	require.NoError(t, err)
	require.EqualValues(t, 100, sent, "only the second report's 100 moved")
}

// A report with no snapshot to subtract is a lifetime total and credits
// nothing. Both edges of a transport losing theirs at once used to re-credit
// days of traffic to each, which min(sent, recv) cannot catch.
func TestBandwidthNoPrevCreditsNothing(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	id := uuid.New().String()
	seedPrev(t, s, id, a)
	seedPrev(t, s, id, b)
	require.NoError(t, s.UpdateBandwidth(ctx, id, a, 400, 10, "webrtc"))
	require.NoError(t, s.UpdateBandwidth(ctx, id, b, 10, 400, "webrtc"))

	require.NoError(t, s.client.Del(ctx, s.bandwidthPrevKey(id, a.Hex()), s.bandwidthPrevKey(id, b.Hex())).Err())
	require.NoError(t, s.UpdateBandwidth(ctx, id, a, 40_000, 1_000, "webrtc"))
	require.NoError(t, s.UpdateBandwidth(ctx, id, b, 1_000, 40_000, "webrtc"))
	h, err := s.client.HGetAll(ctx, s.bandwidthDailyKey(id, time.Now().UTC())).Result()
	require.NoError(t, err)
	require.Equal(t, "400", h[a.Hex()+":sent"])
	require.Equal(t, "400", h[b.Hex()+":recv"])
	require.Equal(t, "410", netDaily(t, s)["bandwidth"])

	require.NoError(t, s.UpdateBandwidth(ctx, id, a, 40_100, 1_000, "webrtc"))
	h, err = s.client.HGetAll(ctx, s.bandwidthDailyKey(id, time.Now().UTC())).Result()
	require.NoError(t, err)
	require.Equal(t, "500", h[a.Hex()+":sent"], "later reports count from the new snapshot")
}
