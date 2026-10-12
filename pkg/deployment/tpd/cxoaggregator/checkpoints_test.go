package cxoaggregator

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/telemetrywire"
)

type checkpointCall struct {
	id       uuid.UUID
	reporter cipher.PubKey
	sent     telemetrywire.DirCheckpoints
}

type checkpointSink struct {
	recordingSink
	calls []checkpointCall
}

func (s *checkpointSink) ApplyCheckpoints(_ context.Context, id uuid.UUID, reporter cipher.PubKey, sent, _ telemetrywire.DirCheckpoints) error {
	s.calls = append(s.calls, checkpointCall{id, reporter, sent})
	return nil
}

// A checkpoint shard on a visor's feed reaches the sink, row by row, for the
// reporter that published it.
func TestDispatchCheckpoints(t *testing.T) {
	sink := &checkpointSink{}
	a := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
	reporter, _ := cipher.GenerateKeyPair()
	e := telemetrywire.CheckpointEntry{ID: uuid.New()}
	e.Sent.Epoch = 3
	sh := telemetrywire.ShardOf(e.ID)

	a.dispatchLeaf(telemetrywire.CheckpointLeafPath(sh), telemetrywire.EncodeCheckpointShard(sh, []telemetrywire.CheckpointEntry{e}), reporter, false)
	require.Equal(t, []checkpointCall{{e.ID, reporter, e.Sent}}, sink.calls)

	// A row filed under the wrong shard is not applied.
	a.dispatchLeaf(telemetrywire.CheckpointLeafPath(sh^1), telemetrywire.EncodeCheckpointShard(sh^1, []telemetrywire.CheckpointEntry{e}), reporter, false)
	require.Len(t, sink.calls, 1)
}
