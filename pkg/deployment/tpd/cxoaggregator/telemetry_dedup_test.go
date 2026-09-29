package cxoaggregator

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/telemetrywire"
)

// A Root re-delivers every shard; an unchanged one is applied once, and a
// transport's uptime heartbeat comes at most once per heartbeatEvery,
// whichever edge reports it.
func TestTelemetryShardAppliedOnceHeartbeatPaced(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	shard := uint8(2)
	mk := func(low byte) uuid.UUID {
		var id uuid.UUID
		id[0] = shard << 4
		id[15] = low
		return id
	}
	entries := []telemetrywire.Entry{
		{ID: mk(1), SentBytes: 100, RecvBytes: 50, LatMin: 1, LatMax: 3, LatAvg: 2, SampledAtUnix: 1_800_000_000, Type: telemetrywire.TypeSTCPR},
		{ID: mk(2), SentBytes: 7, RecvBytes: 8, SampledAtUnix: 1_800_000_000, Type: telemetrywire.TypeSUDPH},
	}
	blob := telemetrywire.EncodeShard(shard, entries)

	sink := &recordingSink{}
	agg := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
	path := telemetrywire.LeafPath(shard)

	agg.dispatchTelemetryShard(path, blob, a)
	agg.dispatchTelemetryShard(path, blob, a) // heartbeat Root: same bytes
	if sink.bandwidths != 2 || len(sink.heartbeats) != 2 {
		t.Fatalf("after a repeat: bandwidths=%d heartbeats=%d, want 2 and 2", sink.bandwidths, len(sink.heartbeats))
	}

	// The other edge's shard is its own data, but the same transports' uptime.
	agg.dispatchTelemetryShard(path, blob, b)
	if sink.bandwidths != 4 || len(sink.heartbeats) != 2 {
		t.Fatalf("other edge: bandwidths=%d heartbeats=%d, want 4 and 2", sink.bandwidths, len(sink.heartbeats))
	}

	// A heartbeat interval later an unchanged Root proves them up again, now.
	agg.tel.mu.Lock()
	for id := range agg.tel.beats {
		agg.tel.beats[id] = time.Now().Add(-heartbeatEvery)
	}
	agg.tel.mu.Unlock()
	before := time.Now().UTC()
	agg.dispatchTelemetryShard(path, blob, a)
	if sink.bandwidths != 4 || len(sink.heartbeats) != 4 {
		t.Fatalf("interval later: bandwidths=%d heartbeats=%d, want 4 and 4", sink.bandwidths, len(sink.heartbeats))
	}
	if at := sink.heartbeats[3].at; at.Before(before) {
		t.Errorf("unchanged-shard heartbeat dated %v, want now (>= %v)", at, before)
	}

	// Changed content is applied.
	entries[0].SentBytes = 500
	agg.dispatchTelemetryShard(path, telemetrywire.EncodeShard(shard, entries), a)
	if sink.bandwidths != 6 {
		t.Fatalf("changed shard: bandwidths=%d, want 6", sink.bandwidths)
	}
}
