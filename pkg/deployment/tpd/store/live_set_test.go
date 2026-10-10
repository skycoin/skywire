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

// The by-edge reads follow the set: either edge finds a transport, and a
// delete, a changed edge pair or a lapse drops it from the old edges.
func TestLiveSetByEdge(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	c, _ := cipher.GenerateKeyPair()
	now := time.Now()
	l := &liveSet{on: true, ttl: time.Minute,
		tps:  map[uuid.UUID]*liveTransport{},
		edge: map[cipher.PubKey]map[uuid.UUID]struct{}{},
		lat:  map[uuid.UUID]liveValue{}, tput: map[uuid.UUID]liveValue{}}
	ab := &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "stcpr"}
	bc := &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(b, c), Type: "sudph"}
	l.put([]*transport.Entry{ab, bc}, now)
	l.setLatency(ab.ID, 7, now)

	ids := func(pk cipher.PubKey, qos bool) map[uuid.UUID]float64 {
		es, ok := l.byEdge(pk, qos, now)
		require.True(t, ok)
		m := map[uuid.UUID]float64{}
		for _, e := range es {
			m[e.ID] = e.Latency
		}
		return m
	}
	require.Equal(t, map[uuid.UUID]float64{ab.ID: 0, bc.ID: 0}, ids(b, false))
	require.Equal(t, map[uuid.UUID]float64{ab.ID: 7}, ids(a, true))

	// The same ID re-registered between other edges leaves a.
	moved := *ab
	moved.Edges = transport.SortEdges(b, c)
	l.put([]*transport.Entry{&moved}, now)
	require.Empty(t, ids(a, false))
	require.Len(t, ids(c, false), 2)

	l.del(bc.ID)
	require.Equal(t, map[uuid.UUID]float64{ab.ID: 0}, ids(c, false))

	l.sweep(now.Add(2 * time.Minute))
	require.Empty(t, ids(b, false))
	require.Empty(t, l.edge, "the index empties with the set")

	_, ok := (&liveSet{}).byEdge(a, false, now)
	require.False(t, ok, "off: callers read redis")
}

func TestShareStore(t *testing.T) {
	const url = "redis://share-store-test:6379"
	ctx, cancel := context.WithCancel(context.Background())
	s := newMemoryStore()
	ShareStore(ctx, url, s)
	if got, ok := SharedLiveStore(url); !ok || got != Store(s) {
		t.Fatalf("SharedLiveStore = %v, %v; want the shared store", got, ok)
	}
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := SharedLiveStore(url); !ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("store still shared after ctx ended")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
