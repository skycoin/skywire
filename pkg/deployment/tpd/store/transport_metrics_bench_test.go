// Package store pkg/deployment/tpd/store/transport_metrics_bench_test.go c4-net-discovery
package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// BenchmarkGetAllTransportMetrics reads 5 days of metrics for 5000 transports
// among 200 visors, the shape of the network where each visor has many
// transports. It needs SKYWIRE_TEST_REDIS, like the redis store tests.
func BenchmarkGetAllTransportMetrics(b *testing.B) {
	s := newTestRedisStore(b)
	ctx := context.Background()
	pks := make([]cipher.PubKey, 200)
	for i := range pks {
		pks[i], _ = cipher.GenerateKeyPair()
	}
	seed := s.client.Pipeline()
	for i := 0; i < 5000; i++ {
		id := uuid.New().String()
		a, c := pks[i%len(pks)], pks[(i*7+1)%len(pks)]
		for d := 0; d < 3; d++ {
			seed.HSet(ctx, s.bandwidthDailyKey(id, time.Now().UTC().AddDate(0, 0, -d)), a.Hex()+":sent", "700", c.Hex()+":recv", "300")
		}
		seed.Set(ctx, s.bandwidthEdgesKey(id), a.Hex()+","+c.Hex()+",stcpr", 0)
	}
	if _, err := seed.Exec(ctx); err != nil {
		b.Fatal(err)
	}
	q := MetricsQuery{Days: 5, Live: "all", Edges: true, Bandwidth: true, Latency: true}
	b.ReportAllocs()
	for b.Loop() {
		m, err := s.GetAllTransportMetrics(ctx, q)
		if err != nil || len(m) != 5000 {
			b.Fatalf("got %d metrics, %v", len(m), err)
		}
	}
}
