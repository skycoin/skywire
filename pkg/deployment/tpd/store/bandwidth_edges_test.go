package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

// TestParseBandwidthEdgePair covers the round-trip of the value written by
// bandwidthEdgesKey at registration and read back in recoverBandwidthEdges —
// the mechanism that keeps an expired transport's counterparty identifiable
// even when only one edge ever published bandwidth.
func TestParseBandwidthEdgePair(t *testing.T) {
	pk0, _ := cipher.GenerateKeyPair()
	pk1, _ := cipher.GenerateKeyPair()

	t.Run("valid pair round-trips in order", func(t *testing.T) {
		edges, typ, ok := parseBandwidthEdgePair(pk0.Hex() + "," + pk1.Hex())
		require.True(t, ok)
		assert.Equal(t, pk0, edges[0])
		assert.Equal(t, pk1, edges[1])
		assert.Empty(t, typ)
	})

	t.Run("keeps the type", func(t *testing.T) {
		e := &transport.Entry{Edges: [2]cipher.PubKey{pk0, pk1}, Type: "stcpr"}
		edges, typ, ok := parseBandwidthEdgePair(bandwidthEdgesValue(e))
		require.True(t, ok)
		assert.Equal(t, e.Edges, edges)
		assert.Equal(t, "stcpr", typ)
	})

	t.Run("rejects wrong field count", func(t *testing.T) {
		_, _, ok := parseBandwidthEdgePair(pk0.Hex())
		assert.False(t, ok)
		_, _, ok = parseBandwidthEdgePair(pk0.Hex() + "," + pk1.Hex() + ",stcpr," + pk0.Hex())
		assert.False(t, ok)
	})

	t.Run("rejects malformed hex", func(t *testing.T) {
		_, _, ok := parseBandwidthEdgePair("not-a-pk," + pk1.Hex())
		assert.False(t, ok)
	})

	t.Run("rejects empty", func(t *testing.T) {
		_, _, ok := parseBandwidthEdgePair("")
		assert.False(t, ok)
	})
}

// A transport that expires keeps its type in the rebuilt entry: from the
// bw:edges value, or, for a value written before the type was kept, from the
// day's uptime hash.
func TestExpiredTransportKeepsType(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	e1 := &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "sudph"}
	e2 := &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "stcpr"}
	require.NoError(t, s.RegisterTransportsBatch(ctx, cipher.PubKey{}, []*transport.SignedEntry{{Entry: e1}, {Entry: e2}}))
	require.NoError(t, s.UpdateBandwidth(ctx, e1.ID.String(), a, 100, 0, "sudph"))
	require.NoError(t, s.UpdateBandwidth(ctx, e2.ID.String(), a, 10, 0, "stcpr"))
	require.NoError(t, s.RecordTransportHeartbeat(ctx, e2.ID, "stcpr", time.Now()))
	// e2's pair as written before the type was kept.
	require.NoError(t, s.client.Set(ctx, s.bandwidthEdgesKey(e2.ID.String()), e2.Edges[0].Hex()+","+e2.Edges[1].Hex(), time.Hour).Err())

	entries, expired, err := s.expiredTransportEntries(ctx, map[uuid.UUID]bool{}, 1)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	got := map[uuid.UUID]string{}
	for _, e := range entries {
		require.True(t, expired[e.ID])
		got[e.ID] = string(e.Type)
	}
	assert.Equal(t, map[uuid.UUID]string{e1.ID: "sudph", e2.ID: "stcpr"}, got)
}
