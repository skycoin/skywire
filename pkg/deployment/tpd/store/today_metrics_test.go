package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

func todayRow(t *testing.T, rows []TransportMetric, id uuid.UUID) TransportMetric {
	t.Helper()
	for _, r := range rows {
		if r.ID == id.String() {
			return r
		}
	}
	t.Fatalf("no row for %s", id)
	return TransportMetric{}
}

func rowBandwidth(r TransportMetric) uint64 {
	var n uint64
	for _, d := range r.Daily {
		if d.A != nil {
			n += d.A.Sent + d.A.Recv
		}
		if d.B != nil {
			n += d.B.Sent + d.B.Recv
		}
	}
	return n
}

// After the first read of the day, only transports whose figures were written
// are re-read; the rest carry over, and liveness follows the registered set.
func TestTodayMetricsRereadsOnlyWhatMoved(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	e1 := &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "stcpr"}
	e2 := &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "sudph"}
	require.NoError(t, s.RegisterTransportsBatch(ctx, cipher.PubKey{}, []*transport.SignedEntry{{Entry: e1}, {Entry: e2}}))
	require.NoError(t, s.UpdateBandwidth(ctx, e1.ID.String(), a, 100, 0, "stcpr"))
	require.NoError(t, s.UpdateBandwidth(ctx, e2.ID.String(), a, 10, 0, "sudph"))

	rows, err := s.GetTodayTransportMetrics(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 100, rowBandwidth(todayRow(t, rows, e1.ID)))
	require.EqualValues(t, 10, rowBandwidth(todayRow(t, rows, e2.ID)))
	require.Empty(t, s.today.takeDirty(), "the full read consumes the marks")

	// Only e1 moves.
	require.NoError(t, s.UpdateBandwidth(ctx, e1.ID.String(), a, 150, 0, "stcpr"))
	s.today.mu.Lock()
	require.Len(t, s.today.dirty, 1)
	s.today.mu.Unlock()

	rows, err = s.GetTodayTransportMetrics(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 150, rowBandwidth(todayRow(t, rows, e1.ID)))
	require.EqualValues(t, 10, rowBandwidth(todayRow(t, rows, e2.ID)), "unchanged row carried over")
	require.True(t, todayRow(t, rows, e2.ID).Live)

	// e2 is deregistered: its row stays for the day, no longer live.
	require.NoError(t, s.DeregisterTransport(ctx, e2.ID))
	s.allTpsCache = newAllTransportsCache(defaultAllTransportsCacheTTL) // drop the 5 s cache
	rows, err = s.GetTodayTransportMetrics(ctx)
	require.NoError(t, err)
	require.False(t, todayRow(t, rows, e2.ID).Live)
	require.EqualValues(t, 10, rowBandwidth(todayRow(t, rows, e2.ID)))
}

func TestMetricsLeafSaveLoad(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	require.NoError(t, s.SaveMetricsLeaf(ctx, "2026-09-01", [][]byte{[]byte("a"), []byte("b")}))
	require.NoError(t, s.SaveMetricsLeaf(ctx, "2026-09-02", [][]byte{[]byte("c")}))
	require.NoError(t, s.SaveMetricsLeaf(ctx, "2026-09-02", [][]byte{[]byte("d")}), "a re-save replaces")

	got, err := s.LoadMetricsLeaves(ctx, []string{"2026-09-01", "2026-09-02", "2026-09-03"})
	require.NoError(t, err)
	require.Equal(t, [][]byte{[]byte("a"), []byte("b")}, got["2026-09-01"])
	require.Equal(t, [][]byte{[]byte("d")}, got["2026-09-02"])
	_, has := got["2026-09-03"]
	require.False(t, has)
}
