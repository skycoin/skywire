package telemetrywire

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCheckpointShardRoundTrip(t *testing.T) {
	e := CheckpointEntry{ID: uuid.New()}
	e.Sent.Epoch, e.Sent.Latest[0] = 7, Checkpoint{Offset: 16 << 20, Digest: 99}
	e.Recv.Epoch, e.Recv.Latest[3] = 8, Checkpoint{Offset: 64 << 30, Digest: 1}
	sh := ShardOf(e.ID)
	b := EncodeCheckpointShard(sh, []CheckpointEntry{e, {ID: uuid.New()}})
	gotSh, got, err := DecodeCheckpointShard(b)
	require.NoError(t, err)
	require.Equal(t, sh, gotSh)
	require.Len(t, got, 2)
	require.Equal(t, e, got[0])

	_, _, err = DecodeCheckpointShard(b[:len(b)-1])
	require.ErrorIs(t, err, ErrLengthMismatch)
	b[0] = Version
	_, _, err = DecodeCheckpointShard(b)
	require.ErrorIs(t, err, ErrVersion, "a telemetry shard is not a checkpoint shard")
}

func TestCheckpointLeafPath(t *testing.T) {
	for sh := uint8(0); sh < ShardCount; sh++ {
		got, ok := ParseCheckpointLeafPath(CheckpointLeafPath(sh))
		require.True(t, ok)
		require.Equal(t, sh, got)
	}
	for _, p := range []string{LeafPath(1), "transports/checkpoints/10", "transports/checkpoints/0f/x", "transports/checkpoints/F"} {
		_, ok := ParseCheckpointLeafPath(p)
		require.False(t, ok, p)
	}
}
