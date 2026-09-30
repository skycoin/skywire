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

// Once loaded, the whole-set reads come from memory and follow this
// store's writes: register, latency, throughput peak, touch, deregister,
// lapse — and the index sets are pruned of what lapsed.
func TestLiveSetFollowsWrites(t *testing.T) {
	s := newTestRedisStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	seeded := &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "stcpr"}
	require.NoError(t, s.RegisterTransportsBatch(ctx, a, []*transport.SignedEntry{{Entry: seeded}}))
	require.NoError(t, s.UpdateLatency(ctx, seeded.ID.String(), 5, 15, 10, "stcpr"))

	url := "redis://live-test/" + uuid.NewString()
	require.NoError(t, s.EnableLiveSet(ctx, url))
	shared, ok := SharedLiveStore(url)
	require.True(t, ok)
	require.Same(t, Store(s), shared)

	got, err := s.GetAllTransportsWithLatency(ctx, false)
	require.NoError(t, err)
	require.Len(t, got, 1, "loaded from redis")
	require.EqualValues(t, 10, got[0].Latency)

	fresh := &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "sudph"}
	require.NoError(t, s.RegisterTransportsBatch(ctx, b, []*transport.SignedEntry{{Entry: fresh}}))
	require.NoError(t, s.ApplyTelemetry(ctx, []TelemetryUpdate{{ID: fresh.ID, Reporter: b, ThroughputBps: 900, LatMin: 1, LatMax: 3, LatAvg: 2}}))
	require.NoError(t, s.UpdateThroughput(ctx, fresh.ID.String(), b, 100)) // lower: peak kept

	// Nothing reads redis now: empty the transport keys and the set still answers.
	require.NoError(t, s.client.Del(ctx, s.transportKey(seeded.ID), s.transportKey(fresh.ID)).Err())
	got, err = s.GetAllTransportsWithLatency(ctx, false)
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, e := range got {
		if e.ID == fresh.ID {
			require.EqualValues(t, 2, e.Latency)
			require.EqualValues(t, 900, e.ThroughputBps)
		}
	}

	removed, err := s.DeregisterTransports(ctx, []uuid.UUID{seeded.ID})
	require.NoError(t, err)
	require.Empty(t, removed, "its redis key was deleted above")
	s.live.del(seeded.ID)
	got, err = s.GetAllTransports(ctx, false)
	require.NoError(t, err)
	require.Len(t, got, 1)

	// Lapse without a touch: swept, and pruned from the index sets.
	gone := s.live.sweep(time.Now().Add(s.live.ttl + time.Second))
	require.Len(t, gone, 1)
	s.pruneIndexes(ctx, gone)
	in, err := s.client.SIsMember(ctx, s.allTpsIndexKey(), fresh.ID.String()).Result()
	require.NoError(t, err)
	require.False(t, in, "the lapsed transport is pruned from the index")
	in, err = s.client.SIsMember(ctx, s.edgeKey(b), fresh.ID.String()).Result()
	require.NoError(t, err)
	require.False(t, in)
	got, err = s.GetAllTransports(ctx, false)
	require.NoError(t, err)
	require.Empty(t, got)
}

// A touched transport does not lapse.
func TestLiveSetTouchKeeps(t *testing.T) {
	var l liveSet
	l.on, l.ttl = true, time.Minute
	l.tps = map[uuid.UUID]*liveTransport{}
	l.lat, l.tput = map[uuid.UUID]liveValue{}, map[uuid.UUID]liveValue{}
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	e := &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "stcpr"}
	t0 := time.Now()
	l.put([]*transport.Entry{e}, t0)
	l.touch([]uuid.UUID{e.ID}, t0.Add(50*time.Second))
	require.Empty(t, l.sweep(t0.Add(90*time.Second)))
	require.Len(t, l.sweep(t0.Add(111*time.Second)), 1)
}
