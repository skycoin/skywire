// Package visor pkg/visor/init_stats_prune_test.go
//
// Pins the telemetry-feed live-only invariant: after a visor restart the
// CXO publisher hydrates its in-memory tree from the previously-published
// Root, bringing back transports/<id>/current leaves for transports that
// closed while the visor was down. pruneDeadCurrentLeaves must reconcile
// every such dead leaf away at startup — not just the ones (re-)written
// since the restart — so the discovery feed TPD fills stays truly
// live-only and doesn't accumulate stale-transport bloat across reboots.
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
)

func currentPath(id uuid.UUID) string { return "transports/" + id.String() + "/current" }

func TestPruneDeadCurrentLeavesAfterHydrate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cxo-stats")
	_, sk := cipher.GenerateKeyPair()
	log := logging.MustGetLogger("test-stats-prune")

	live1, live2 := uuid.New(), uuid.New()
	dead1, dead2 := uuid.New(), uuid.New()

	// Session 1: publish two live + two dead current leaves, then close so
	// the Root persists to the on-disk CXO container.
	pub1, err := treestore.NewWithTCP("127.0.0.1:0", sk, treestore.PubConfig{
		DataDir:     dir,
		BatchWindow: 5 * time.Millisecond,
	})
	require.NoError(t, err, "session 1 publisher")
	for _, id := range []uuid.UUID{live1, live2, dead1, dead2} {
		require.NoError(t, pub1.Put(currentPath(id), []byte(`{"sent_bytes":1}`)))
	}
	require.NoError(t, pub1.Flush())
	require.NoError(t, pub1.Close())

	// Session 2: reopen the same DataDir. hydrateFromContainer rebuilds the
	// in-memory tree with all four current leaves — the post-restart state
	// in which dead1/dead2 belong to transports that are no longer live.
	pub2, err := treestore.NewWithTCP("127.0.0.1:0", sk, treestore.PubConfig{
		DataDir:     dir,
		BatchWindow: 5 * time.Millisecond,
	})
	require.NoError(t, err, "session 2 publisher")
	defer func() { _ = pub2.Close() }()

	// Sanity: hydrate brought all four leaves back onto the feed.
	sink := &cxoSink{pub: pub2, log: log}
	liveIDs := map[uuid.UUID]struct{}{live1: {}, live2: {}}
	require.Equal(t, CurrentLeafStats{Total: 4, Live: 2, Dead: 2},
		currentLeafStats(pub2, liveIDs), "all four current leaves present after hydrate")

	// Prune against the actual live set.
	require.Equal(t, 2, pruneDeadCurrentLeaves(pub2, sink, liveIDs), "both dead leaves pruned")

	// Only the live leaves remain.
	require.Equal(t, CurrentLeafStats{Total: 2, Live: 2, Dead: 0},
		currentLeafStats(pub2, liveIDs), "only live current leaves remain after prune")
	for _, id := range []uuid.UUID{live1, live2} {
		_, ok := pub2.Get(currentPath(id))
		require.True(t, ok, "live leaf %s should remain", id)
	}
	for _, id := range []uuid.UUID{dead1, dead2} {
		_, ok := pub2.Get(currentPath(id))
		require.False(t, ok, "dead leaf %s should be pruned", id)
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
