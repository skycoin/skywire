package cxoaggregator

import (
	"context"
	"testing"
	"time"

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

func ckAggregator(at *time.Time) (*Aggregator, *checkpointSink) {
	sink := &checkpointSink{}
	return &Aggregator{sink: sink, log: logging.MustGetLogger("test"), ckClock: func() time.Time { return *at }}, sink
}

func ckShard(e telemetrywire.CheckpointEntry) (string, []byte) {
	sh := telemetrywire.ShardOf(e.ID)
	return telemetrywire.CheckpointLeafPath(sh), telemetrywire.EncodeCheckpointShard(sh, []telemetrywire.CheckpointEntry{e})
}

// A checkpoint shard on a visor's feed reaches the sink, row by row, for the
// reporter that published it.
func TestDispatchCheckpoints(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 30, 0, time.UTC)
	a, sink := ckAggregator(&now)
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

func TestCheckpointPhases(t *testing.T) {
	day := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		at   time.Duration
		open bool
	}{
		{0, true},
		{checkpointOpen - time.Second, true},
		{checkpointOpen, false},
		{checkpointCycle - time.Second, false},
		{checkpointCycle, true},
		{12*time.Hour + 5*time.Minute, false},
		{24*time.Hour - checkpointOpen - time.Second, false},
		{24*time.Hour - checkpointOpen, true},
		{24*time.Hour - time.Second, true},
	} {
		require.Equal(t, c.open, checkpointPhaseOpen(day.Add(c.at)), "at %v", c.at)
	}
}

// Between phases each edge's newest row is held, and both are applied
// together when the next phase opens, so the match compares the same minute.
func TestCheckpointsHeldBetweenPhases(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 5, 0, 0, time.UTC)
	a, sink := ckAggregator(&now)
	edgeA, _ := cipher.GenerateKeyPair()
	edgeB, _ := cipher.GenerateKeyPair()
	id := uuid.New()
	row := func(epoch, off uint64) telemetrywire.CheckpointEntry {
		e := telemetrywire.CheckpointEntry{ID: id}
		e.Sent.Epoch, e.Recv.Epoch = epoch, epoch
		e.Sent.Recent[0] = telemetrywire.Checkpoint{Offset: off, Digest: off * 7}
		return e
	}

	for m := uint64(5); m < 9; m++ {
		p, b := ckShard(row(1, m))
		a.dispatchCheckpoints(p, b, edgeA)
		p, b = ckShard(row(1, m+100))
		a.dispatchCheckpoints(p, b, edgeB)
		now = now.Add(time.Minute)
	}
	require.Empty(t, sink.calls, "closed phase: held")
	a.flushCheckpoints()
	require.Empty(t, sink.calls, "still closed")

	now = time.Date(2026, 10, 16, 12, 10, 10, 0, time.UTC)
	a.flushCheckpoints()
	require.Len(t, sink.calls, 2, "one row per edge, the newest")
	for _, c := range sink.calls {
		if c.reporter == edgeA {
			require.EqualValues(t, 8, c.sent.Recent[0].Offset)
		} else {
			require.EqualValues(t, 108, c.sent.Recent[0].Offset)
		}
	}

	// Open phase: rows are applied as they come.
	p, b := ckShard(row(1, 10))
	a.dispatchCheckpoints(p, b, edgeA)
	require.Len(t, sink.calls, 3)
	a.flushCheckpoints()
	require.Len(t, sink.calls, 3, "nothing left held")
}

// The last minutes of a UTC day are open, so its verified bytes count on it.
func TestCheckpointsAppliedBeforeMidnight(t *testing.T) {
	now := time.Date(2026, 10, 16, 23, 55, 0, 0, time.UTC)
	a, sink := ckAggregator(&now)
	edge, _ := cipher.GenerateKeyPair()
	e := telemetrywire.CheckpointEntry{ID: uuid.New()}
	e.Sent.Epoch = 1
	p, b := ckShard(e)
	a.dispatchCheckpoints(p, b, edge)
	require.Empty(t, sink.calls)
	now = time.Date(2026, 10, 16, 23, 58, 30, 0, time.UTC)
	a.flushCheckpoints()
	require.Len(t, sink.calls, 1)
}

// A shard repeating the bytes last taken is skipped until checkpointRefresh.
func TestCheckpointsUnchangedShardSkipped(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	a, sink := ckAggregator(&now)
	edge, _ := cipher.GenerateKeyPair()
	e := telemetrywire.CheckpointEntry{ID: uuid.New()}
	e.Sent.Epoch = 1
	p, b := ckShard(e)
	a.dispatchCheckpoints(p, b, edge)
	now = now.Add(30 * time.Second)
	a.dispatchCheckpoints(p, b, edge)
	require.Len(t, sink.calls, 1)
	now = now.Add(checkpointRefresh)
	now = now.Truncate(checkpointCycle)
	a.dispatchCheckpoints(p, b, edge)
	require.Len(t, sink.calls, 2, "refreshed so the store's TTL does not lapse")
}
