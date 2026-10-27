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

func ids(es []*transport.Entry) map[uuid.UUID]bool {
	m := map[uuid.UUID]bool{}
	for _, e := range es {
		m[e.ID] = true
	}
	return m
}

// GetTransportsByEdgeExcept lists what GetTransportsByEdgeNoLatency lists
// minus the keep set, from redis and from the live set alike, and reports
// not found only when the edge has nothing.
func TestGetTransportsByEdgeExcept(t *testing.T) {
	s := newTestRedisStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, _ := cipher.GenerateKeyPair()
	lonely, _ := cipher.GenerateKeyPair()
	var signed []*transport.SignedEntry
	for i := 0; i < 5; i++ {
		b, _ := cipher.GenerateKeyPair()
		signed = append(signed, &transport.SignedEntry{Entry: &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "stcpr"}})
	}
	require.NoError(t, s.RegisterTransportsBatch(ctx, a, signed))
	keep := map[uuid.UUID]struct{}{signed[0].Entry.ID: {}, signed[3].Entry.ID: {}}
	want := map[uuid.UUID]bool{signed[1].Entry.ID: true, signed[2].Entry.ID: true, signed[4].Entry.ID: true}

	check := func() {
		got, err := s.GetTransportsByEdgeExcept(ctx, a, keep)
		require.NoError(t, err)
		require.Equal(t, want, ids(got))
		all := map[uuid.UUID]struct{}{}
		for _, se := range signed {
			all[se.Entry.ID] = struct{}{}
		}
		got, err = s.GetTransportsByEdgeExcept(ctx, a, all)
		require.NoError(t, err, "listed but all kept is not not-found")
		require.Empty(t, got)
		_, err = s.GetTransportsByEdgeExcept(ctx, lonely, keep)
		require.ErrorIs(t, err, ErrTransportNotFound)
	}
	check()
	require.NoError(t, s.EnableLiveSet(ctx, "redis://except-test/"+uuid.NewString()))
	check()
}

func TestLiveSetByEdgeExcept(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	now := time.Now()
	l := &liveSet{on: true, ttl: time.Minute,
		tps:  map[uuid.UUID]*liveTransport{},
		edge: map[cipher.PubKey]map[uuid.UUID]struct{}{},
		lat:  map[uuid.UUID]liveValue{}, tput: map[uuid.UUID]liveValue{}}
	var es []*transport.Entry
	for i := 0; i < 4; i++ {
		b, _ := cipher.GenerateKeyPair()
		es = append(es, &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "stcpr"})
	}
	l.put(es, now)
	l.setLatency(es[1].ID, 9, now)
	out, n, ok := l.byEdgeExcept(a, map[uuid.UUID]struct{}{es[0].ID: {}}, now)
	require.True(t, ok)
	require.Equal(t, 4, n)
	require.Equal(t, map[uuid.UUID]bool{es[1].ID: true, es[2].ID: true, es[3].ID: true}, ids(out))
	for _, e := range out {
		require.Zero(t, e.Latency, "no QoS overlay, as NoLatency")
	}
	out[0].Type = "changed"
	again, _ := l.byEdge(a, false, now)
	for _, e := range again {
		require.NotEqual(t, "changed", string(e.Type), "results are copies")
	}
	_, n, _ = l.byEdgeExcept(a, nil, now.Add(2*time.Minute))
	require.Zero(t, n, "lapsed transports are not counted")
	_, _, ok = (&liveSet{}).byEdgeExcept(a, nil, now)
	require.False(t, ok)
}

func BenchmarkLiveSetByEdge(b *testing.B) {
	a, _ := cipher.GenerateKeyPair()
	now := time.Now()
	l := &liveSet{on: true, ttl: time.Hour,
		tps:  map[uuid.UUID]*liveTransport{},
		edge: map[cipher.PubKey]map[uuid.UUID]struct{}{},
		lat:  map[uuid.UUID]liveValue{}, tput: map[uuid.UUID]liveValue{}}
	es := make([]*transport.Entry, 500)
	keep := map[uuid.UUID]struct{}{}
	for i := range es {
		pk, _ := cipher.GenerateKeyPair()
		es[i] = &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, pk), Type: "stcpr"}
		if i%50 != 0 {
			keep[es[i].ID] = struct{}{}
		}
	}
	l.put(es, now)
	b.Run("byEdge", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			l.byEdge(a, false, now)
		}
	})
	b.Run("byEdgeExcept", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			l.byEdgeExcept(a, keep, now)
		}
	})
}
