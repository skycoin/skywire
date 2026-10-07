// Package store pkg/deployment/tpd/store/redis_batched_pipe_test.go c4-net-discovery
package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Reads spanning several pipeline batches all come back, and a failed batch is
// an error rather than keys that look empty.
func TestBatchedPipe(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	day := time.Now().UTC().AddDate(0, 0, -3)
	const n = pipelineBatch*2 + 7
	seed := s.client.Pipeline()
	for i := 0; i < n; i++ {
		id := uuid.New().String()
		seed.HSet(ctx, s.bandwidthDailyKey(id, day), a.Hex()+":sent", "700", b.Hex()+":sent", "300")
		seed.Set(ctx, s.bandwidthEdgesKey(id), a.Hex()+","+b.Hex(), 0)
	}
	_, err := seed.Exec(ctx)
	require.NoError(t, err)

	metrics, err := s.GetAllTransportMetrics(ctx, MetricsQuery{Days: 5, Live: "all", Edges: true, Bandwidth: true, Latency: true})
	require.NoError(t, err)
	require.Len(t, metrics, n)
	for _, m := range metrics {
		require.Len(t, m.Daily, 1)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	p := s.batchedPipe(canceled)
	p.next().Get(canceled, "missing")
	require.Error(t, p.exec())
}
