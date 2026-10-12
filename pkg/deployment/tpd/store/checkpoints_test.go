package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/telemetrywire"
	"github.com/skycoin/skywire/pkg/transport"
)

// dir is a direction with the given epoch and finest checkpoints.
func dir(epoch uint64, pts ...telemetrywire.Checkpoint) telemetrywire.DirCheckpoints {
	d := telemetrywire.DirCheckpoints{Epoch: epoch}
	copy(d.Recent[telemetrywire.CheckpointRecent-len(pts):], pts)
	return d
}

func cp(mib, digest uint64) telemetrywire.Checkpoint {
	return telemetrywire.Checkpoint{Offset: mib << 20, Digest: digest}
}

// Checkpoints both edges report for the same conn verify the sender's bytes
// up to the furthest one they share; the metrics read carries it, and the pay
// rule caps by it.
func TestApplyCheckpointsVerifies(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	e := &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "stcpr"}
	require.NoError(t, s.RegisterTransportsBatch(ctx, cipher.PubKey{}, []*transport.SignedEntry{{Entry: e}}))
	ea, eb := e.Edges[0], e.Edges[1]
	const gib = 1 << 30
	require.NoError(t, s.UpdateBandwidth(ctx, e.ID.String(), ea, 4*gib, 0, "stcpr"))
	require.NoError(t, s.UpdateBandwidth(ctx, e.ID.String(), eb, 0, 4*gib, "stcpr"))

	// The sender is ahead of the receiver; they share 32 and 48 MiB.
	require.NoError(t, s.ApplyCheckpoints(ctx, e.ID, ea, dir(7, cp(32, 2), cp(48, 3), cp(64, 4)), dir(0)))
	require.NoError(t, s.ApplyCheckpoints(ctx, e.ID, eb, dir(0), dir(7, cp(16, 1), cp(32, 2), cp(48, 3))))
	// A later report adds only what moved.
	require.NoError(t, s.ApplyCheckpoints(ctx, e.ID, eb, dir(0), dir(7, cp(48, 3), cp(64, 4))))

	got, err := s.GetTransportMetricsByIDs(ctx, []uuid.UUID{e.ID}, MetricsQuery{Days: 1, Bandwidth: true})
	require.NoError(t, err)
	require.Len(t, got, 1)
	day := got[0].Daily[0]
	require.True(t, day.A.Checked)
	require.EqualValues(t, 64<<20, day.A.Verified)
	require.False(t, day.B.Checked, "B sent nothing with checkpoints")

	a1, b1 := SenderBytes(day)
	require.EqualValues(t, 64<<20+256<<20, a1, "4 GiB counted, 64 MiB verified: paid up to the slack")
	require.Zero(t, b1)
}

// A different digest at a shared offset is a mismatch and verifies nothing,
// and two different conns cannot be compared at all.
func TestApplyCheckpointsMismatchAndEpoch(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	id := uuid.New()
	key := s.bandwidthDailyKey(id.String(), time.Now().UTC())

	require.NoError(t, s.ApplyCheckpoints(ctx, id, a, dir(9, cp(16, 1)), dir(0)))
	require.NoError(t, s.ApplyCheckpoints(ctx, id, b, dir(0), dir(8, cp(16, 1))))
	h, err := s.client.HGetAll(ctx, key).Result()
	require.NoError(t, err)
	require.NotContains(t, h, a.Hex()+":vck", "different conns are not compared")

	require.NoError(t, s.ApplyCheckpoints(ctx, id, b, dir(0), dir(9, cp(16, 5))))
	require.NoError(t, s.ApplyCheckpoints(ctx, id, b, dir(0), dir(9, cp(16, 5))))
	h, err = s.client.HGetAll(ctx, key).Result()
	require.NoError(t, err)
	require.Equal(t, "1", h[a.Hex()+":vck"])
	require.Equal(t, "1", h[a.Hex()+":vbad"], "counted once per conn")
	require.NotContains(t, h, a.Hex()+":vsent")
	ttl, err := s.client.TTL(ctx, key).Result()
	require.NoError(t, err)
	require.Positive(t, ttl, "the daily hash expires")
}
