// Package router pkg/router/route_mux_legs_test.go
package router

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// muxLegIDs returns the transport ID on each of the mux's leg records.
func muxLegIDs(m *routeMux) []uuid.UUID {
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	ids := make([]uuid.UUID, len(m.legs))
	for i, l := range m.legs {
		ids[i] = l.id
	}
	return ids
}

// requireMuxInStep checks the mux holds one record per leg, in leg order.
func requireMuxInStep(t *testing.T, rg *RouteGroup) {
	t.Helper()
	rg.mu.Lock()
	want := rg.legIDsLocked()
	rg.mu.Unlock()
	require.Equal(t, want, muxLegIDs(rg.mux))
}

// TestMuxLegRecordsFollowLegs checks a leg's record, with its counters and
// flags, stays on that leg's transport through an append, a swap and a removal.
func TestMuxLegRecordsFollowLegs(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 3)

	// An append names every record, the ones the rig built unnamed included.
	id := uuid.New()
	conn := newWorkingTransport()
	t.Cleanup(func() { conn.Close() }) //nolint:errcheck,gosec
	mt := transport.NewManagedTransportForTest(conn)
	mt.Entry = transport.Entry{ID: id, Type: "test"}
	pk1, pk2 := rg.desc.SrcPK(), rg.desc.DstPK()
	fwd := routing.ForwardRule(DefaultRouteKeepAlive, 4, 103, id, pk2, pk1, 1, 2)
	rvs := routing.ConsumeRule(DefaultRouteKeepAlive, 103, pk1, pk2, 2, 1)
	rg.appendRules(fwd, rvs, mt, "test")
	requireMuxInStep(t, rg)

	rec := rg.mux.legAt(3)
	require.Equal(t, id, rec.id)
	require.False(t, rec.ready, "a new aux leg starts not-ready")
	require.False(t, rec.standby)

	_, _, ok := rg.setLegStandbyByID(mts[2].Entry.ID, true)
	require.True(t, ok)
	parked := rg.mux.legAt(2)
	parked.sentBytes.Store(7)

	rg.reelectPrimary(mts[1].Entry.ID)
	requireMuxInStep(t, rg)
	require.Same(t, parked, rg.mux.legAt(2))

	require.Equal(t, 1, rg.removeLegsByID([]uuid.UUID{mts[1].Entry.ID}, "test", "removed"))
	requireMuxInStep(t, rg)
	// mts[0] took slot 0, and the parked leg moved down with its state.
	require.Equal(t, []uuid.UUID{mts[0].Entry.ID, mts[2].Entry.ID, id}, muxLegIDs(rg.mux))
	require.Same(t, parked, rg.mux.legAt(1))
	require.True(t, rg.mux.isLegStandby(1))
	require.EqualValues(t, 7, rg.mux.legAt(1).sentBytes.Load())
	require.True(t, rg.mux.legReadyAt(0))
	require.False(t, rg.mux.isLegStandby(0))
}

// TestMuxNewLegDefaults checks the add-time state of a leg record.
func TestMuxNewLegDefaults(t *testing.T) {
	m := newBareMux(false)
	m.SetStandbyNewLegs(true)
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	m.syncLegs(ids)
	require.Equal(t, ids, muxLegIDs(m))
	ready, standby := m.legFlags()
	require.Equal(t, []bool{true, false, false}, ready, "only leg 0 is born ready")
	require.Equal(t, []bool{false, true, true}, standby, "leg 0 is never standby")

	// A park of a leg the mux has not been told of yet is kept, and the leg is
	// named when it is.
	m.setLegStandby(4, false)
	_, standby = m.legFlags()
	require.Equal(t, []bool{false, true, true, true, false}, standby)
	ids = append(ids, uuid.New(), uuid.New())
	m.syncLegs(ids)
	require.Equal(t, ids, muxLegIDs(m))
}
