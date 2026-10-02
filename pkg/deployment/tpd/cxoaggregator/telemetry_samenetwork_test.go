package cxoaggregator

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/telemetrywire"
	"github.com/skycoin/skywire/pkg/transport"
)

// batchCapture is a recordingSink that also batches, capturing the updates.
type batchCapture struct {
	recordingSink
	updates []store.TelemetryUpdate
}

func (b *batchCapture) ApplyTelemetry(_ context.Context, u []store.TelemetryUpdate) error {
	b.updates = append(b.updates, u...)
	return nil
}

func (b *batchCapture) RecordTransportHeartbeats(context.Context, []*transport.Entry, time.Time) error {
	return nil
}

// A telemetry entry flagged same-network reaches the store marked; an
// unflagged one does not.
func TestTelemetrySameNetworkReachesStore(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	shard := uint8(3)
	mk := func(low byte) uuid.UUID {
		var id uuid.UUID
		id[0] = shard << 4
		id[15] = low
		return id
	}
	lan, wan := mk(1), mk(2)
	blob := telemetrywire.EncodeShard(shard, []telemetrywire.Entry{
		{ID: lan, SentBytes: 100, Type: telemetrywire.TypeSTCPR | telemetrywire.TypeFlagSameNetwork},
		{ID: wan, SentBytes: 100, Type: telemetrywire.TypeSTCPR},
	})
	sink := &batchCapture{}
	agg := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
	agg.dispatchTelemetryShard(telemetrywire.LeafPath(shard), blob, a)

	marked := map[uuid.UUID]bool{}
	for _, u := range sink.updates {
		marked[u.ID] = u.SameNetwork
		require.Equal(t, "stcpr", u.Type, "the flag does not hide the type")
	}
	require.Equal(t, map[uuid.UUID]bool{lan: true, wan: false}, marked)
}
