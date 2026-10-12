// Package cxoaggregator pkg/deployment/tpd/cxoaggregator/checkpoints.go c4-net-discovery
package cxoaggregator

import (
	"context"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/telemetrywire"
)

// CheckpointSink is a Sink that matches transports' stream checkpoints.
type CheckpointSink interface {
	ApplyCheckpoints(ctx context.Context, id uuid.UUID, reporter cipher.PubKey, sent, recv telemetrywire.DirCheckpoints) error
}

// dispatchCheckpoints applies one transports/checkpoints/<sh> shard.
func (a *Aggregator) dispatchCheckpoints(path string, leaf []byte, reporter cipher.PubKey) {
	sink, ok := a.sink.(CheckpointSink)
	if !ok {
		return
	}
	shard, entries, err := telemetrywire.DecodeCheckpointShard(leaf)
	if err != nil {
		a.log.WithError(err).WithField("path", path).Debug("CXO aggregator: checkpoint shard decode failed")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), telemetryShardTimeout)
	defer cancel()
	for _, e := range entries {
		if telemetrywire.ShardOf(e.ID) != shard {
			continue
		}
		if err := sink.ApplyCheckpoints(ctx, e.ID, reporter, e.Sent, e.Recv); err != nil {
			a.log.WithError(err).WithField("transport", e.ID).Debug("CXO aggregator: ApplyCheckpoints failed")
			if ctx.Err() != nil {
				return
			}
		}
	}
}
