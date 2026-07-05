// Package store — memory_store_test.go: exhaustive coverage of the
// in-memory TransportStore (the test double used across the tpd API
// tests) and the New() factory. The redis-backed implementations need a
// live redis / miniredis and are not covered here.
package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func memEntry(t *testing.T, ty types.Type) (*transport.Entry, cipher.PubKey, cipher.PubKey) {
	t.Helper()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	return &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: ty}, a, b
}

func signed(e *transport.Entry) *transport.SignedEntry {
	return &transport.SignedEntry{Entry: e}
}

func TestNewStore(t *testing.T) {
	ctx := context.Background()
	log := logging.MustGetLogger("t")

	s, err := New(ctx, storeconfig.Config{Type: storeconfig.Memory}, time.Minute, log)
	require.NoError(t, err)
	require.NotNil(t, s)

	_, err = New(ctx, storeconfig.Config{Type: storeconfig.Type(99)}, time.Minute, log)
	require.Error(t, err) // unknown store type
}

func TestMemoryStore_CRUD(t *testing.T) {
	ctx := context.Background()
	s := newMemoryStore()

	e1, a1, _ := memEntry(t, types.STCPR)
	e2, a2, _ := memEntry(t, types.SUDPH)

	// Batch register.
	require.NoError(t, s.RegisterTransportsBatch(ctx, cipher.PubKey{}, []*transport.SignedEntry{signed(e1), signed(e2)}))

	// GetByID.
	got, err := s.GetTransportByID(ctx, e1.ID)
	require.NoError(t, err)
	require.Equal(t, e1, got)
	_, err = s.GetTransportByID(ctx, uuid.New())
	require.ErrorIs(t, err, ErrTransportNotFound)

	// GetByEdge / NoLatency.
	byEdge, err := s.GetTransportsByEdge(ctx, a1)
	require.NoError(t, err)
	require.Len(t, byEdge, 1)
	_, err = s.GetTransportsByEdgeNoLatency(ctx, mustGenPK(t))
	require.ErrorIs(t, err, ErrTransportNotFound)

	// Counts + listings.
	counts, err := s.GetNumberOfTransports(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, counts[types.STCPR])
	require.Equal(t, 1, counts[types.SUDPH])

	all, err := s.GetAllTransports(ctx, true)
	require.NoError(t, err)
	require.Len(t, all, 2)

	// Deregister.
	require.NoError(t, s.DeregisterTransport(ctx, e1.ID))
	require.ErrorIs(t, s.DeregisterTransport(ctx, e1.ID), ErrTransportNotFound)
	_ = a2

	s.Close()
}

func TestMemoryStore_RegisterErrors(t *testing.T) {
	ctx := context.Background()
	s := newMemoryStore()

	// nil entry → ErrBadEntry.
	require.ErrorIs(t, s.RegisterTransport(ctx, cipher.PubKey{}, &transport.SignedEntry{}), ErrBadEntry)

	// Injected error short-circuits every method.
	boom := errString("boom")
	s.SetError(boom)
	e, _, _ := memEntry(t, types.STCPR)
	require.ErrorIs(t, s.RegisterTransport(ctx, cipher.PubKey{}, signed(e)), boom)
	require.ErrorIs(t, s.RegisterTransportsBatch(ctx, cipher.PubKey{}, []*transport.SignedEntry{signed(e)}), boom)
	require.ErrorIs(t, s.DeregisterTransport(ctx, e.ID), boom)
	_, err := s.GetTransportByID(ctx, e.ID)
	require.ErrorIs(t, err, boom)
	_, err = s.GetTransportsByEdgeNoLatency(ctx, mustGenPK(t))
	require.ErrorIs(t, err, boom)
	s.SetError(nil)
}

func TestMemoryStore_SelfTransportFilter(t *testing.T) {
	ctx := context.Background()
	s := newMemoryStore()

	// A self-transport (both edges identical) is excluded when
	// selfTransports=false but included when true.
	self, _ := cipher.GenerateKeyPair()
	selfEntry := &transport.Entry{ID: uuid.New(), Edges: [2]cipher.PubKey{self, self}, Type: types.STCPR}
	require.NoError(t, s.RegisterTransport(ctx, cipher.PubKey{}, signed(selfEntry)))

	withSelf, err := s.GetAllTransports(ctx, true)
	require.NoError(t, err)
	require.Len(t, withSelf, 1)

	withoutSelf, err := s.GetAllTransports(ctx, false)
	require.NoError(t, err)
	require.Empty(t, withoutSelf)
}

func TestMemoryStore_NoopAndEmptyMethods(t *testing.T) {
	ctx := context.Background()
	s := newMemoryStore()
	pk := mustGenPK(t)
	id := uuid.New()

	require.NoError(t, s.UpdateBandwidth(ctx, "tp", pk, 1, 2))
	require.NoError(t, s.UpdateLatency(ctx, "tp", 1, 2, 3))
	require.NoError(t, s.RecordHeartbeat(ctx, pk, "v1"))
	require.NoError(t, s.RecordTransportHeartbeat(ctx, id, "stcpr", time.Now()))
	require.NoError(t, s.IngestTransportTimeline(ctx, id, "stcpr", nil))
	require.NoError(t, s.BackupAndCleanOldBandwidth(ctx, "/tmp/x"))

	bw, err := s.GetTransportBandwidth(ctx, id, "h", 1)
	require.NoError(t, err)
	require.Empty(t, bw)
	vbw, err := s.GetVisorBandwidth(ctx, pk, "h", 1)
	require.NoError(t, err)
	require.Empty(t, vbw)

	require.Nil(t, s.GetDailyTimeline(ctx, "h", time.Now()))
	require.Nil(t, s.GetTransportDailyTimeline(ctx, "h", time.Now()))

	up, err := s.GetTransportUptimeSummaries(ctx, []uuid.UUID{id}, true, true)
	require.NoError(t, err)
	require.Empty(t, up)
	upv, err := s.GetTransportUptimeByVisor(ctx, pk, true, true)
	require.NoError(t, err)
	require.Empty(t, upv)
}

func TestMemoryStore_VisorSummariesAndMetrics(t *testing.T) {
	ctx := context.Background()
	s := newMemoryStore()
	e1, a1, b1 := memEntry(t, types.STCPR)
	e2, _, _ := memEntry(t, types.SUDPH)
	require.NoError(t, s.RegisterTransport(ctx, cipher.PubKey{}, signed(e1)))
	require.NoError(t, s.RegisterTransport(ctx, cipher.PubKey{}, signed(e2)))

	// Every edge of every transport appears as an online visor.
	summaries, err := s.GetAllVisorSummaries(ctx, false, false)
	require.NoError(t, err)
	require.Len(t, summaries, 4) // 2 transports × 2 distinct edges
	for _, su := range summaries {
		require.True(t, su.Online)
	}

	// Network + visor aggregate metrics return the empty-but-shaped responses.
	nm, err := s.GetNetworkMetrics(ctx, MetricsQuery{})
	require.NoError(t, err)
	require.NotNil(t, nm)
	require.NotNil(t, nm.Cumulative)

	vm, err := s.GetVisorAggregateMetrics(ctx, []cipher.PubKey{a1, b1}, MetricsQuery{})
	require.NoError(t, err)
	require.Len(t, vm, 2)

	// Transport metrics: the memory store has no latency/bandwidth, so
	// buildTransportMetrics filters everything out — but the type-filter,
	// edges, and lookup paths still run.
	_, err = s.GetAllTransportMetrics(ctx, MetricsQuery{})
	require.NoError(t, err)
	_, err = s.GetAllTransportMetrics(ctx, MetricsQuery{Type: "stcpr", Edges: true})
	require.NoError(t, err)
	_, err = s.GetAllTransportMetrics(ctx, MetricsQuery{Type: "no-such-type"})
	require.NoError(t, err)

	byIDs, err := s.GetTransportMetricsByIDs(ctx, []uuid.UUID{e1.ID, uuid.New()}, MetricsQuery{Edges: true})
	require.NoError(t, err)
	require.Empty(t, byIDs) // filtered (no metrics) but lookups ran

	byVisors, err := s.GetTransportMetricsByVisors(ctx, []cipher.PubKey{a1}, MetricsQuery{})
	require.NoError(t, err)
	require.Empty(t, byVisors)
}

// --- helpers ---------------------------------------------------------

type errString string

func (e errString) Error() string { return string(e) }

func mustGenPK(t *testing.T) cipher.PubKey {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return pk
}
