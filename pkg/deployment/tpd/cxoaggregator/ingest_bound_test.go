package cxoaggregator

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/telemetrywire"
)

// slowSink holds every UpdateBandwidth for delay and records the peak
// number of calls in flight at once.
type slowSink struct {
	recordingSink
	delay    time.Duration
	inFlight atomic.Int32
	peak     atomic.Int32
}

func (s *slowSink) UpdateBandwidth(ctx context.Context, id string, pk cipher.PubKey, sent, recv uint64) error {
	n := s.inFlight.Add(1)
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			break
		}
	}
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
	}
	s.inFlight.Add(-1)
	return s.recordingSink.UpdateBandwidth(ctx, id, pk, sent, recv)
}

func shardBlob(shard uint8, rows int) []byte {
	entries := make([]telemetrywire.Entry, rows)
	for i := range entries {
		var id uuid.UUID
		id[0] = shard << 4
		id[15] = byte(i + 1)
		entries[i] = telemetrywire.Entry{ID: id, SentBytes: 1, RecvBytes: 1, Type: telemetrywire.TypeSTCPR}
	}
	return telemetrywire.EncodeShard(shard, entries)
}

// TestIngestIsBounded: many feeds dispatching at once never have more than
// maxConcurrentIngest leaves writing to the store.
func TestIngestIsBounded(t *testing.T) {
	sink := &slowSink{delay: 20 * time.Millisecond}
	a := &Aggregator{sink: sink, log: logging.MustGetLogger("test"),
		ingest: make(chan struct{}, maxConcurrentIngest)}
	blob := shardBlob(1, 1)
	var wg sync.WaitGroup
	for i := 0; i < 4*maxConcurrentIngest; i++ {
		reporter, _ := cipher.GenerateKeyPair()
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.dispatchLeaf(telemetrywire.LeafPath(1), blob, reporter, false)
		}()
	}
	wg.Wait()
	if got := sink.peak.Load(); got > maxConcurrentIngest {
		t.Fatalf("peak concurrent store writes = %d, want <= %d", got, maxConcurrentIngest)
	}
	if sink.bandwidths != 4*maxConcurrentIngest {
		t.Fatalf("bandwidth writes = %d, want %d", sink.bandwidths, 4*maxConcurrentIngest)
	}
}

// TestTelemetryShardStopsWhenOutOfTime: once the shard's budget is spent the
// remaining rows are skipped instead of each failing against a dead context.
func TestTelemetryShardStopsWhenOutOfTime(t *testing.T) {
	prev := telemetryShardTimeout
	telemetryShardTimeout = 30 * time.Millisecond
	defer func() { telemetryShardTimeout = prev }()

	sink := &slowSink{delay: 20 * time.Millisecond}
	a := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
	reporter, _ := cipher.GenerateKeyPair()
	a.dispatchTelemetryShard(telemetrywire.LeafPath(2), shardBlob(2, 50), reporter)
	if sink.bandwidths == 0 || sink.bandwidths >= 50 {
		t.Fatalf("bandwidth writes = %d, want some but not all 50", sink.bandwidths)
	}
	if len(sink.heartbeats) > sink.bandwidths {
		t.Fatalf("heartbeats = %d after %d bandwidth writes", len(sink.heartbeats), sink.bandwidths)
	}
}
