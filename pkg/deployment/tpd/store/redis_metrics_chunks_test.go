package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// More daily hashes than one chunk holds must come out whole and in day order.
func TestMetricsAcrossChunks(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	now := time.Now().UTC()
	n := metricsChunkCmds/2 + 50
	seed := s.client.Pipeline()
	for i := 0; i < n; i++ {
		id := uuid.New().String()
		seed.Set(ctx, s.bandwidthEdgesKey(id), a.Hex()+","+b.Hex()+",stcpr", 0)
		for d := 0; d < 3; d++ {
			seed.HSet(ctx, s.bandwidthDailyKey(id, now.AddDate(0, 0, -d)), a.Hex()+":sent", 100+d, b.Hex()+":recv", 7)
		}
	}
	_, err := seed.Exec(ctx)
	require.NoError(t, err)

	m, err := s.GetAllTransportMetrics(ctx, MetricsQuery{Days: 5, Live: "all", Bandwidth: true})
	require.NoError(t, err)
	require.Len(t, m, n)
	for _, tm := range m {
		require.Len(t, tm.Daily, 3)
		for d, row := range tm.Daily {
			require.Equal(t, now.AddDate(0, 0, -d).Format(MetricsDateFormat), row.Date)
			require.Equal(t, uint64(100+d), row.A.Sent)
			require.Equal(t, uint64(7), row.B.Recv)
		}
	}
}
