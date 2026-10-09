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
		require.InDelta(t, tpUptimeTimelineTTL, ttl(tpUptimeTimelineKey(id.String(), date)), float64(time.Minute))
	}
	require.InDelta(t, tpUptimeTodayTTL, ttl(tpUptimeOnlineKey(date)), float64(time.Minute))
}
