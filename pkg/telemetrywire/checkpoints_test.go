package telemetrywire

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCheckpointShardRoundTrip(t *testing.T) {
	e := CheckpointEntry{ID: uuid.New()}
	e.Sent.Epoch, e.Sent.Recent[CheckpointRecent-1] = 7, Checkpoint{Offset: 16 << 20, Digest: 99}
	e.Recv.Epoch, e.Recv.Coarse[2] = 8, Checkpoint{Offset: 64 << 30, Digest: 1}
	sh := ShardOf(e.ID)
	b := EncodeCheckpointShard(sh, []CheckpointEntry{e, {ID: uuid.New()}})
	gotSh, got, err := DecodeCheckpointShard(b)
	require.NoError(t, err)
	require.Equal(t, sh, gotSh)
	require.Len(t, got, 2)
	require.Equal(t, e, got[0])

	_, _, err = DecodeCheckpointShard(b[:len(b)-1])
	require.ErrorIs(t, err, ErrLengthMismatch)
	b[0] = 0x01
	_, _, err = DecodeCheckpointShard(b)
	require.ErrorIs(t, err, ErrVersion, "the first checkpoint format is not read")
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

func TestDirCheckpointsPoints(t *testing.T) {
	var d DirCheckpoints
	require.Empty(t, d.Points())
	d.Recent[CheckpointRecent-1] = Checkpoint{Offset: 32 << 20, Digest: 2}
	d.Recent[CheckpointRecent-2] = Checkpoint{Offset: 16 << 20, Digest: 1}
	d.Coarse[0] = Checkpoint{Offset: 256 << 20, Digest: 3}
	require.Equal(t, []Checkpoint{{16 << 20, 1}, {32 << 20, 2}, {256 << 20, 3}}, d.Points())
}
