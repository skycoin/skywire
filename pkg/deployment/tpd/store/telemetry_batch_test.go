package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// A batch does what the single-item writes do: bandwidth totals, the
// throughput peak kept, the latency record — and survives redis losing its
// script cache.
func TestApplyTelemetryBatch(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	tp1, tp2 := uuid.New(), uuid.New()

	require.NoError(t, s.ApplyTelemetry(ctx, []TelemetryUpdate{
		{ID: tp1, Reporter: a, Sent: 100, Recv: 50, ThroughputBps: 500, LatMin: 5, LatMax: 15, LatAvg: 10, Type: "stcpr"},
		{ID: tp2, Reporter: a, Sent: 10, Type: "dmsg"},
	}))
	require.Equal(t, "160", netDaily(t, s)["bandwidth"])

	// Redis restarts and forgets its scripts; the batch still applies.
	require.NoError(t, s.client.ScriptFlush(ctx).Err())
	require.NoError(t, s.ApplyTelemetry(ctx, []TelemetryUpdate{
		{ID: tp1, Reporter: b, Sent: 120, Recv: 20, ThroughputBps: 200, Type: "stcpr"}, // lower peak
	}))
	require.Equal(t, "160", netDaily(t, s)["bandwidth"], "b's 140 is under tp1's max of 150")

	tput, err := s.getThroughputRecord(ctx, tp1)
	require.NoError(t, err)
	require.EqualValues(t, 500, tput.Bps, "the running peak is kept")
	raw, err := s.client.Get(ctx, s.latencyKey(tp1)).Result()
	require.NoError(t, err)
	require.Contains(t, raw, `"avg":10000`)
}

// A report marking the peer as on the reporter's own network marks that
// transport's day, alongside its counters and expiring with them; an unmarked
// transport's day is not marked.
func TestApplyTelemetrySameNetwork(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	a, _ := cipher.GenerateKeyPair()
	lan, wan := uuid.New(), uuid.New()

	require.NoError(t, s.ApplyTelemetry(ctx, []TelemetryUpdate{
		{ID: lan, Reporter: a, Sent: 100, Recv: 50, Type: "stcpr", SameNetwork: true},
		{ID: wan, Reporter: a, Sent: 100, Recv: 50, Type: "stcpr"},
	}))
	now := time.Now().UTC()
	marked, err := s.client.HGet(ctx, s.bandwidthDailyKey(lan.String(), now), sameNetworkField).Result()
	require.NoError(t, err)
	require.Equal(t, "1", marked)
	ttl, err := s.client.TTL(ctx, s.bandwidthDailyKey(lan.String(), now)).Result()
	require.NoError(t, err)
	require.Greater(t, ttl, time.Duration(0), "the day's hash still expires")
	n, err := s.client.HExists(ctx, s.bandwidthDailyKey(wan.String(), now), sameNetworkField).Result()
	require.NoError(t, err)
	require.False(t, n)
}
