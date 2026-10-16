package cxoaggregator

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/telemetrywire"
)

// busyRecord is an edge's checkpoints at a stream position: the last
// CheckpointRecent multiples of 16 MiB at or below it.
func busyRecord(id uuid.UUID, pos uint64) telemetrywire.CheckpointEntry {
	const step = 16 << 20
	e := telemetrywire.CheckpointEntry{ID: id}
	e.Sent.Epoch, e.Recv.Epoch = 1, 1
	top := pos / step
	for i := 0; i < telemetrywire.CheckpointRecent && top > uint64(i); i++ {
		off := (top - uint64(i)) * step
		cp := telemetrywire.Checkpoint{Offset: off, Digest: off ^ 0x5eed}
		e.Sent.Recent[telemetrywire.CheckpointRecent-1-i] = cp
		e.Recv.Recent[telemetrywire.CheckpointRecent-1-i] = cp
	}
	return e
}

// On a link far busier than the 128 MiB the recent checkpoints span per
// minute, the paced rows of both edges still match, against the real
// script: they are applied in the same phase.
func TestPacedCheckpointsStillMatchOnABusyLink(t *testing.T) {
	url := os.Getenv("SKYWIRE_TEST_REDIS")
	if url == "" {
		t.Skip("SKYWIRE_TEST_REDIS unset; no redis to test against")
	}
	ctx := context.Background()
	opt, err := redis.ParseURL(url)
	require.NoError(t, err)
	rc := redis.NewClient(opt)
	defer rc.Close() //nolint:errcheck
	require.NoError(t, rc.FlushDB(ctx).Err())
	defer rc.FlushDB(ctx) //nolint:errcheck
	st, err := store.New(ctx, storeconfig.Config{Type: storeconfig.Redis, URL: url, PoolSize: 4}, time.Minute, logging.MustGetLogger("test"))
	require.NoError(t, err)
	defer st.Close()

	now := time.Date(2026, 10, 16, 12, 4, 0, 0, time.UTC)
	agg := &Aggregator{sink: &aggSinkWithCheckpoints{st: st}, log: logging.MustGetLogger("test"), ckClock: func() time.Time { return now }}
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	id := uuid.New()
	const perMinute = 2400 << 20 // 40 MiB/s
	for m := 0; m < 9; m++ {
		pos := uint64(m+1) * perMinute
		now = time.Date(2026, 10, 16, 12, 5+m, 5, 0, time.UTC)
		agg.flushCheckpoints()
		p, blob := ckShard(busyRecord(id, pos))
		agg.dispatchCheckpoints(p, blob, a)
		now = now.Add(30 * time.Second)
		agg.flushCheckpoints()
		p, blob = ckShard(busyRecord(id, pos-(4<<20))) // the receiver trails a little
		agg.dispatchCheckpoints(p, blob, b)
	}
	day := rc.HGetAll(ctx, "transport-discovery:bw:daily:"+id.String()+":"+time.Now().UTC().Format("2006-01-02")).Val()
	require.Equal(t, "1", day[a.Hex()+":vck"])
	require.NotEmpty(t, day[a.Hex()+":vsent"], "matched across the phase")

	// What pacing each edge on its own clock would do: records minutes apart
	// share no offset, and nothing is verified.
	require.NoError(t, rc.FlushDB(ctx).Err())
	id2 := uuid.New()
	sa, sb := busyRecord(id2, 10*perMinute), busyRecord(id2, 13*perMinute)
	require.NoError(t, st.(CheckpointSink).ApplyCheckpoints(ctx, id2, a, sa.Sent, sa.Recv))
	require.NoError(t, st.(CheckpointSink).ApplyCheckpoints(ctx, id2, b, sb.Sent, sb.Recv))
	day = rc.HGetAll(ctx, "transport-discovery:bw:daily:"+id2.String()+":"+time.Now().UTC().Format("2006-01-02")).Val()
	require.Empty(t, day[a.Hex()+":vsent"])
}

type aggSinkWithCheckpoints struct {
	recordingSink
	st store.TransportStore
}

func (s *aggSinkWithCheckpoints) ApplyCheckpoints(ctx context.Context, id uuid.UUID, reporter cipher.PubKey, sent, recv telemetrywire.DirCheckpoints) error {
	return s.st.(CheckpointSink).ApplyCheckpoints(ctx, id, reporter, sent, recv)
}
