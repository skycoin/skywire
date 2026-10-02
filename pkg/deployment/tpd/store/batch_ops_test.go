package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

func TestBatchOpsTouchDeregisterHeartbeat(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	e1 := &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "stcpr"}
	e2 := &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "sudph"}
	require.NoError(t, s.RegisterTransportsBatch(ctx, a, []*transport.SignedEntry{{Entry: e1}, {Entry: e2}}))

	// Touch resets the lifetime without rewriting.
	require.NoError(t, s.client.Expire(ctx, s.transportKey(e1.ID), 5*time.Second).Err())
	require.NoError(t, s.TouchTransports(ctx, a, []uuid.UUID{e1.ID, e2.ID}))
	ttl, err := s.client.TTL(ctx, s.transportKey(e1.ID)).Result()
	require.NoError(t, err)
	require.Greater(t, ttl, 30*time.Second)

	// Heartbeats: one pipeline, once per slot.
	require.NoError(t, s.RecordTransportHeartbeats(ctx, []*transport.Entry{e1, e2}, time.Time{}))
	date := time.Now().UTC().Format("2006-01-02")
	n, err := s.client.SCard(ctx, tpUptimeOnlineKey(date)).Result()
	require.NoError(t, err)
	require.EqualValues(t, 2, n)

	// Deregister both, plus one that never existed.
	removed, err := s.DeregisterTransports(ctx, []uuid.UUID{e1.ID, e2.ID, uuid.New()})
	require.NoError(t, err)
	require.Len(t, removed, 2)
	_, err = s.GetTransportByID(ctx, e1.ID)
	require.ErrorIs(t, err, ErrTransportNotFound)
	members, err := s.client.SMembers(ctx, s.edgeKey(a)).Result()
	require.NoError(t, err)
	require.Empty(t, members)
}
