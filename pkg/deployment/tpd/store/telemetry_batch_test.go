package store

import (
	"context"
	"strconv"
	"strings"
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

// dumpHashes returns every hash and set in the db, without updated_at.
func dumpHashes(t *testing.T, s *redisStore) map[string]map[string]string {
	t.Helper()
	ctx := context.Background()
	out := map[string]map[string]string{}
	keys, err := s.client.Keys(ctx, "*").Result()
	require.NoError(t, err)
	for _, k := range keys {
		switch s.client.Type(ctx, k).Val() {
		case "hash":
			h, err := s.client.HGetAll(ctx, k).Result()
			require.NoError(t, err)
			delete(h, "updated_at")
			out[k] = h
		case "set":
			m, err := s.client.SMembers(ctx, k).Result()
			require.NoError(t, err)
			h := map[string]string{}
			for _, v := range m {
				h[v] = ""
			}
			out[k] = h
		}
	}
	return out
}

// The batched scripts leave redis exactly as the single-row writes do, with
// many rows from a few reporters folded into one call each.
func TestApplyTelemetryBatchMatchesSingleRows(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	reporters := make([]cipher.PubKey, 3)
	for i := range reporters {
		reporters[i], _ = cipher.GenerateKeyPair()
	}
	ids := make([]uuid.UUID, 300)
	for i := range ids {
		ids[i] = uuid.New()
	}
	types := []string{"stcpr", "sudph", ""}
	var rounds [][]TelemetryUpdate
	for r := 0; r < 4; r++ {
		var batch []TelemetryUpdate
		for i, id := range ids {
			u := TelemetryUpdate{ID: id, Reporter: reporters[i%3], Type: types[i%3],
				Sent: uint64((r + 1) * (i + 1) * 1000), Recv: uint64((r + 1) * i * 10)}
			if i%5 == 0 {
				u.Sent = 0 // a reset counter
			}
			if i%4 == 0 {
				u.LatMin, u.LatMax, u.LatAvg = 1, 9, float64(r+i%7)+0.25
			}
			batch = append(batch, u)
		}
		rounds = append(rounds, batch)
	}

	for _, b := range rounds {
		require.NoError(t, s.ApplyTelemetry(ctx, b))
	}
	batched := dumpHashes(t, s)
	require.NoError(t, s.client.FlushDB(ctx).Err())
	for _, b := range rounds {
		for _, u := range b {
			require.NoError(t, s.UpdateBandwidth(ctx, u.ID.String(), u.Reporter, u.Sent, u.Recv, u.Type))
			if latencyValid(u.LatMin, u.LatMax, u.LatAvg) {
				require.NoError(t, s.UpdateLatency(ctx, u.ID.String(), u.LatMin, u.LatMax, u.LatAvg, u.Type))
			}
		}
	}
	single := dumpHashes(t, s)
	require.Equal(t, len(single), len(batched))
	for k, want := range single {
		got := batched[k]
		require.Len(t, got, len(want), k)
		for f, v := range want {
			if a, err := strconv.ParseFloat(v, 64); err == nil && strings.Contains(f, "lat_sum") {
				b, err := strconv.ParseFloat(got[f], 64)
				require.NoError(t, err, "%s %s", k, f)
				require.InDelta(t, a, b, 1e-6, "%s %s", k, f)
				continue
			}
			require.Equal(t, v, got[f], "%s %s", k, f)
		}
	}
}

// A row held across UTC midnight is counted on the day it arrived.
func TestApplyTelemetryDatedByArrival(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	a, _ := cipher.GenerateKeyPair()
	id := uuid.New()
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	require.NoError(t, s.ApplyTelemetry(ctx, []TelemetryUpdate{
		{ID: id, Reporter: a, Sent: 100, Recv: 10, Type: "stcpr", At: yesterday, LatMin: 1, LatMax: 3, LatAvg: 2},
	}))
	day := yesterday.Format(MetricsDateFormat)
	n, err := s.client.Exists(ctx, s.bandwidthDailyKey(id.String(), time.Now().UTC())).Result()
	require.NoError(t, err)
	require.Zero(t, n, "nothing on today's hash")
	got, err := s.client.HGet(ctx, s.bandwidthDailyKey(id.String(), yesterday), a.Hex()+":sent").Result()
	require.NoError(t, err)
	require.Equal(t, "100", got)
	got, err = s.client.HGet(ctx, s.netDailyKey(day), "bandwidth").Result()
	require.NoError(t, err)
	require.Equal(t, "110", got)
	got, err = s.client.HGet(ctx, s.visorBandwidthDailyKey(a.Hex(), yesterday), "sent").Result()
	require.NoError(t, err)
	require.Equal(t, "100", got)
	got, err = s.client.HGet(ctx, s.netDailyKey(day), "lat_count").Result()
	require.NoError(t, err)
	require.Equal(t, "1", got)

	// Today's next report adds only the delta, to today.
	require.NoError(t, s.ApplyTelemetry(ctx, []TelemetryUpdate{{ID: id, Reporter: a, Sent: 150, Recv: 10, Type: "stcpr"}}))
	got, err = s.client.HGet(ctx, s.bandwidthDailyKey(id.String(), time.Now().UTC()), a.Hex()+":sent").Result()
	require.NoError(t, err)
	require.Equal(t, "50", got)
}
