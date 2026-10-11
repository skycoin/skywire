// Package telemetrywire pkg/telemetrywire/checkpoints.go c3-vis-core
//
// Checkpoint shards carry each transport's stream digest checkpoints (see
// pkg/transport/stream_digest.go) beside the telemetry shards, at
// transports/checkpoints/<sh>, so a TPD that does not read them ignores them.
//
// Wire layout (little-endian): version (1) = 0x01, shard (1), count (2), then
// count entries of 16 + 2*72 bytes: the transport uuid, then for sent and then
// received the epoch (8) and CheckpointLevels checkpoints of offset (8) and
// digest (8).
package telemetrywire

import (
	"encoding/binary"
	"fmt"

	"github.com/google/uuid"
)

// CheckpointVersion is the checkpoint shard format version.
const CheckpointVersion = 0x01

// CheckpointLevels is how many checkpoint levels each direction carries.
const CheckpointLevels = 4

const (
	checkpointDirLen   = 8 + CheckpointLevels*16
	checkpointEntryLen = 16 + 2*checkpointDirLen
)

// Checkpoint is a stream digest at a byte offset of a transport's conn.
type Checkpoint struct {
	Offset uint64
	Digest uint64
}

// DirCheckpoints is one direction of a transport's current conn: the conn's
// epoch and the latest checkpoint of each level.
type DirCheckpoints struct {
	Epoch  uint64
	Latest [CheckpointLevels]Checkpoint
}

// CheckpointEntry is one transport's checkpoints.
type CheckpointEntry struct {
	ID         uuid.UUID
	Sent, Recv DirCheckpoints
}

// HasAny reports whether either direction has reached a checkpoint.
func (e CheckpointEntry) HasAny() bool { return e.Sent.Epoch != 0 || e.Recv.Epoch != 0 }

// CheckpointLeafPath is the CXO leaf path of a checkpoint shard.
func CheckpointLeafPath(shard uint8) string {
	return fmt.Sprintf("transports/checkpoints/%02x", shard)
}

// ParseCheckpointLeafPath returns the shard of a checkpoint leaf path.
func ParseCheckpointLeafPath(path string) (uint8, bool) {
	var sh uint8
	if n, err := fmt.Sscanf(path, "transports/checkpoints/%02x", &sh); err != nil || n != 1 || sh >= ShardCount || CheckpointLeafPath(sh) != path {
		return 0, false
	}
	return sh, true
}

// EncodeCheckpointShard encodes one shard's checkpoint entries.
func EncodeCheckpointShard(shard uint8, entries []CheckpointEntry) []byte {
	b := make([]byte, headerLen+checkpointEntryLen*len(entries))
	b[0], b[1] = CheckpointVersion, shard
	binary.LittleEndian.PutUint16(b[2:4], uint16(len(entries))) //nolint:gosec // a shard holds far fewer than 65536 transports
	off := headerLen
	for _, e := range entries {
		copy(b[off:off+16], e.ID[:])
		off = putDir(b, off+16, e.Sent)
		off = putDir(b, off, e.Recv)
	}
	return b
}

func putDir(b []byte, off int, d DirCheckpoints) int {
	binary.LittleEndian.PutUint64(b[off:], d.Epoch)
	off += 8
	for _, cp := range d.Latest {
		binary.LittleEndian.PutUint64(b[off:], cp.Offset)
		binary.LittleEndian.PutUint64(b[off+8:], cp.Digest)
		off += 16
	}
	return off
}

// DecodeCheckpointShard decodes a checkpoint shard.
func DecodeCheckpointShard(b []byte) (shard uint8, entries []CheckpointEntry, err error) {
	if len(b) < headerLen {
		return 0, nil, ErrShort
	}
	if b[0] != CheckpointVersion {
		return 0, nil, fmt.Errorf("%w: got 0x%02x want 0x%02x", ErrVersion, b[0], CheckpointVersion)
	}
	shard = b[1]
	if shard >= ShardCount {
		return 0, nil, fmt.Errorf("%w: %d", ErrShardRange, shard)
	}
	count := int(binary.LittleEndian.Uint16(b[2:4]))
	if len(b) != headerLen+checkpointEntryLen*count {
		return 0, nil, fmt.Errorf("%w: len=%d count=%d", ErrLengthMismatch, len(b), count)
	}
	entries = make([]CheckpointEntry, count)
	off := headerLen
	for i := range entries {
		copy(entries[i].ID[:], b[off:off+16])
		entries[i].Sent, off = getDir(b, off+16)
		entries[i].Recv, off = getDir(b, off)
	}
	return shard, entries, nil
}

func getDir(b []byte, off int) (DirCheckpoints, int) {
	var d DirCheckpoints
	d.Epoch = binary.LittleEndian.Uint64(b[off:])
	off += 8
	for m := range d.Latest {
		d.Latest[m] = Checkpoint{Offset: binary.LittleEndian.Uint64(b[off:]), Digest: binary.LittleEndian.Uint64(b[off+8:])}
		off += 16
	}
	return d, off
}
