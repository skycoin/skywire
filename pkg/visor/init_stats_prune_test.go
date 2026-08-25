// Package visor pkg/visor/init_stats_prune_test.go
//
// Pins the telemetry-feed startup-prune invariant for the sharded wire
// format: after a visor restart the CXO publisher hydrates its in-memory
// tree from the previously-published Root. On the first run after
// upgrading from the per-transport `current`-leaf format that Root still
// holds LEGACY transports/<id>/current leaves (never republished now), and
// it may hold telemetry shards for transports that have since gone away.
// pruneStaleTelemetryLeaves must delete every legacy current leaf plus any
// shard with no live transport, leaving only the freshly-hydrated live
// shards so the feed TPD fills stays small.
package visor

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/telemetrywire"
)

func currentPath(id uuid.UUID) string { return "transports/" + id.String() + "/current" }

// uuidInShard returns a UUID whose telemetrywire shard is exactly shard.
func uuidInShard(shard uint8) uuid.UUID {
	id := uuid.New()
	id[0] = shard << 4
	return id
}

func TestPruneStaleTelemetryLeavesAfterHydrate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cxo-stats")
	_, sk := cipher.GenerateKeyPair()

	liveID := uuidInShard(1)      // in a live shard — its shard leaf must stay
	deadShardID := uuidInShard(2) // packed into a shard with no live transport
	legacy1, legacy2 := uuid.New(), uuid.New()

	encShard := func(id uuid.UUID) []byte {
		sh := telemetrywire.ShardOf(id)
		return telemetrywire.EncodeShard(sh, []telemetrywire.Entry{{ID: id, SentBytes: 1, Type: telemetrywire.TypeSTCPR}})
	}

	// Session 1: publish a live shard, a dead-only shard, and two legacy
	// per-transport current leaves; close so the Root persists on disk.
	pub1, err := treestore.NewWithTCP("127.0.0.1:0", sk, treestore.PubConfig{
		DataDir:     dir,
		BatchWindow: 5 * time.Millisecond,
	})
	require.NoError(t, err, "session 1 publisher")
	require.NoError(t, pub1.Put(telemetrywire.LeafPath(telemetrywire.ShardOf(liveID)), encShard(liveID)))
	require.NoError(t, pub1.Put(telemetrywire.LeafPath(telemetrywire.ShardOf(deadShardID)), encShard(deadShardID)))
	for _, id := range []uuid.UUID{legacy1, legacy2} {
		require.NoError(t, pub1.Put(currentPath(id), []byte(`{"sent_bytes":1}`)))
	}
	require.NoError(t, pub1.Flush())
	require.NoError(t, pub1.Close())

	// Session 2: reopen the same DataDir — hydrate rebuilds the tree with all
	// leaves, the post-restart state to prune.
	pub2, err := treestore.NewWithTCP("127.0.0.1:0", sk, treestore.PubConfig{
		DataDir:     dir,
		BatchWindow: 5 * time.Millisecond,
	})
	require.NoError(t, err, "session 2 publisher")
	defer func() { _ = pub2.Close() }() //nolint:errcheck // best-effort teardown

	sink := &cxoSink{pub: pub2, log: logging.MustGetLogger("test-stats-prune")}
	liveIDs := map[uuid.UUID]struct{}{liveID: {}}

	// Sanity: the shard leaves decode; the live shard's row is live, the
	// dead shard's row is dead. (Legacy current leaves are not shard leaves,
	// so currentLeafStats — now shard-based — doesn't count them.)
	require.Equal(t, CurrentLeafStats{Total: 2, Live: 1, Dead: 1},
		currentLeafStats(pub2, liveIDs), "one live + one dead shard row after hydrate")

	// Prune: both legacy current leaves + the dead-only shard = 3 deletes.
	require.Equal(t, 3, pruneStaleTelemetryLeaves(pub2, sink, liveIDs))

	// Only the live shard leaf remains.
	require.Equal(t, CurrentLeafStats{Total: 1, Live: 1, Dead: 0},
		currentLeafStats(pub2, liveIDs), "only the live shard remains after prune")
	_, ok := pub2.Get(telemetrywire.LeafPath(telemetrywire.ShardOf(liveID)))
	require.True(t, ok, "live shard leaf should remain")
	_, ok = pub2.Get(telemetrywire.LeafPath(telemetrywire.ShardOf(deadShardID)))
	require.False(t, ok, "dead-only shard leaf should be pruned")
	for _, id := range []uuid.UUID{legacy1, legacy2} {
		_, ok := pub2.Get(currentPath(id))
		require.False(t, ok, "legacy current leaf %s should be pruned", id)
	}
}

func TestCurrentLeafUUIDParsing(t *testing.T) {
	id := uuid.New()
	got, ok := currentLeafUUID("transports/" + id.String() + "/current")
	require.True(t, ok)
	require.Equal(t, id, got)

	for _, bad := range []string{
		"transports/" + id.String(),                 // no /current
		"transports/" + id.String() + "/2026-01-01", // rollup date, not current
		"transports/not-a-uuid/current",
		"tiers/dmsg/2026-01-01",
		"transports/" + id.String() + "/current/extra",
	} {
		_, ok := currentLeafUUID(bad)
		require.False(t, ok, "path %q must not parse as a current leaf", bad)
	}
}

func TestTelemetryShardOfPathParsing(t *testing.T) {
	for shard := uint8(0); shard < telemetrywire.ShardCount; shard++ {
		got, ok := telemetryShardOfPath(telemetrywire.LeafPath(shard))
		require.True(t, ok, "shard %d path must parse", shard)
		require.Equal(t, shard, got)
	}
	for _, bad := range []string{
		"transports/telemetry/",
		"transports/telemetry/0",
		"transports/telemetry/1g",
		"transports/telemetry/100",
		"transports/telemetry/AA", // uppercase not emitted
		"transports/list",
	} {
		_, ok := telemetryShardOfPath(bad)
		require.False(t, ok, "path %q must not parse as a shard leaf", bad)
	}
}
