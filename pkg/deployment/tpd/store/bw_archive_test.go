package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
)

// With the archive kept, a transport's daily hash lives only the live window.
func TestBandwidthDailyTTLWithArchive(t *testing.T) {
	s := newTestRedisStore(t)
	s.SetLeafArchive(t.TempDir())
	ctx := context.Background()
	pk, _ := cipher.GenerateKeyPair()
	id := uuid.New().String()
	require.NoError(t, s.UpdateBandwidth(ctx, id, pk, 100, 50, "stcpr"))
	ttl, err := s.client.TTL(ctx, s.bandwidthDailyKey(id, time.Now().UTC())).Result()
	require.NoError(t, err)
	require.LessOrEqual(t, ttl, bandwidthDailyTTL)
	require.Greater(t, ttl, bandwidthDailyTTL-time.Minute)
}

// A day past the live window is read from its archived leaf, and the cleanup
// removes only the old days that are archived.
func TestBandwidthDaysFromArchive(t *testing.T) {
	s := newTestRedisStore(t)
	s.SetLeafArchive(t.TempDir())
	ctx := context.Background()
	now := time.Now().UTC()
	day := func(back int) string { return now.AddDate(0, 0, -back).Format(MetricsDateFormat) }
	id, other := uuid.New(), uuid.New()

	rec := []TransportMetric{{ID: id.String(), Type: "stcpr", Daily: []DailyEdgeBandwidth{{
		Date: day(5),
		A:    &EdgeBandwidth{Sent: 100, Recv: 50},
		B:    &EdgeBandwidth{Sent: 40, Recv: 20},
	}}}}
	body, err := json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, s.SaveMetricsLeaf(ctx, day(5), [][]byte{cxoutils.Gzip(body)}))

	got, err := s.GetTransportBandwidth(ctx, id, "daily", 7)
	require.NoError(t, err)
	require.Contains(t, got, BandwidthAggregation{TransportID: id.String(), Period: "daily", PeriodKey: day(5), Bandwidth: 150})

	archivedOld := serviceName + ":bw:daily:" + id.String() + ":" + day(5)
	unarchivedOld := serviceName + ":bw:daily:" + other.String() + ":" + day(6)
	live := serviceName + ":bw:daily:" + id.String() + ":" + day(0)
	for _, k := range []string{archivedOld, unarchivedOld, live} {
		require.NoError(t, s.client.HSet(ctx, k, "bandwidth", 1).Err())
	}
	pipe := s.client.Pipeline()
	s.cleanOldBandwidthDaily(ctx, pipe, now)
	_, err = pipe.Exec(ctx)
	require.NoError(t, err)
	n, err := s.client.Exists(ctx, archivedOld, unarchivedOld, live).Result()
	require.NoError(t, err)
	require.Equal(t, int64(2), n, "only the archived old day is deleted")
	require.Zero(t, s.client.Exists(ctx, archivedOld).Val())
}
