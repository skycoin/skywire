package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// The day hash and the online set, read only for today, live 36 hours; the
// timeline, read back a week, keeps eight days. Both write paths agree.
func TestTransportUptimeTTLs(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	date := now.Format(MetricsDateFormat)
	one, two := uuid.New(), uuid.New()
	require.NoError(t, s.RecordTransportHeartbeat(ctx, one, "stcpr", now))
	require.NoError(t, s.RecordTransportHeartbeats(ctx, []*transport.Entry{{ID: two, Type: tptypes.STCPR}}, now))

	ttl := func(k string) time.Duration {
		d, err := s.client.TTL(ctx, k).Result()
		require.NoError(t, err)
		return d
	}
	for _, id := range []uuid.UUID{one, two} {
		require.InDelta(t, tpUptimeTodayTTL, ttl(tpUptimeKey(id.String(), date)), float64(time.Minute))
		require.InDelta(t, tpUptimeTimelineTTL, ttl(tpUptimeTimelineKey(id.String(), date)), float64(time.Minute), "no archive: eight days")
	}
	require.InDelta(t, tpUptimeTodayTTL, ttl(tpUptimeOnlineKey(date)), float64(time.Minute))
}

// A heartbeat sampled on an earlier day still sets that day's timeline slot
// but leaves the day's online set alone, since only today's set is read.
func TestTransportUptimeOldBeatSkipsOnlineSet(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	old := time.Now().UTC().AddDate(0, 0, -3)
	date := old.Format(MetricsDateFormat)
	one, two := uuid.New(), uuid.New()
	require.NoError(t, s.RecordTransportHeartbeat(ctx, one, "stcpr", old))
	require.NoError(t, s.RecordTransportHeartbeats(ctx, []*transport.Entry{{ID: two, Type: tptypes.STCPR}}, old))

	n, err := s.client.Exists(ctx, tpUptimeOnlineKey(date)).Result()
	require.NoError(t, err)
	require.Zero(t, n, "no online set for a past day")
	for _, id := range []uuid.UUID{one, two} {
		bit, err := s.client.GetBit(ctx, tpUptimeTimelineKey(id.String(), date), currentTimelineSlot(old)).Result()
		require.NoError(t, err)
		require.EqualValues(t, 1, bit, "the past day's timeline slot is still set")
	}

	now := time.Now().UTC()
	require.NoError(t, s.RecordTransportHeartbeats(ctx, []*transport.Entry{{ID: two, Type: tptypes.STCPR}}, now))
	members, err := s.client.SMembers(ctx, tpUptimeOnlineKey(now.Format(MetricsDateFormat))).Result()
	require.NoError(t, err)
	require.Equal(t, []string{two.String()}, members)
}
