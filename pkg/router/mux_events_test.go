package router

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// A leg that is added and then goes away must leave both events behind, each
// naming the reason — the whole point of the ring (a live proxy session lost
// its only leg with no record of who removed it or why).
func TestMuxEventsRecordLegAddAndRemoveWithReasons(t *testing.T) {
	r := newLegTestRouter(t)
	rg, _ := r.setupInitializingPrimary(t)
	rg.muxEvents = &r.muxEvents

	// The primary went in before the ring was attached; add two legs now so the
	// prune below still has a survivor (a prune never drops the last leg).
	aux := make([]*transport.ManagedTransport, 0, 2)
	for i := 0; i < 2; i++ {
		rules := r.makeAuxRules(t, rg.desc, i)
		mt := r.tm.Transport(rules.Forward.NextTransportID())
		require.NotNil(t, mt)
		aux = append(aux, mt)
		rg.appendRules(rules.Forward, rules.Reverse, mt, "mux append: aux leg")
	}

	ev := r.MuxEvents()
	require.Len(t, ev, 2)
	require.Equal(t, MuxEventLegAdded, ev[0].Event)
	require.Equal(t, "mux append: aux leg", ev[0].Reason)
	require.Equal(t, MuxByLocal, ev[0].By)
	require.Equal(t, aux[0].Entry.ID, ev[0].TpID)
	require.Equal(t, rg.desc.SrcPK(), ev[0].Desc.SrcPK)
	require.Equal(t, 1, ev[0].LegIndex)
	require.Equal(t, 2, ev[0].Legs)
	require.Equal(t, 3, ev[1].Legs)

	// Kill the last leg's transport; the keep-alive prune reclaims it.
	require.NoError(t, aux[1].Close())
	rg.mu.Lock()
	dropped := rg.pruneDeadTransports()
	rg.mu.Unlock()
	require.Equal(t, []int{2}, dropped)

	ev = r.MuxEvents()
	require.Len(t, ev, 3)
	last := ev[2]
	require.Equal(t, MuxEventLegRemoved, last.Event)
	require.Equal(t, "transport closed", last.Reason)
	require.Equal(t, MuxByLocal, last.By)
	require.Equal(t, aux[1].Entry.ID, last.TpID)
	require.Equal(t, 2, last.LegIndex)
	require.Equal(t, 2, last.Legs)

	// The group's own events ride along in the mux snapshot the CLI reads.
	require.Equal(t, ev, rg.MuxStats().Events)
}

// Removing the leg at index 0 also records the primary re-home, so a reader can
// tell a re-home from a plain leg loss.
func TestMuxEventsRecordPrimaryRehome(t *testing.T) {
	r := newLegTestRouter(t)
	rg, _ := r.setupInitializingPrimary(t)
	rg.muxEvents = &r.muxEvents
	rules := r.makeAuxRules(t, rg.desc, 0)
	mt := r.tm.Transport(rules.Forward.NextTransportID())
	rg.appendRules(rules.Forward, rules.Reverse, mt, "mux append: aux leg")

	require.NoError(t, rg.tps[0].Close())
	rg.mu.Lock()
	rg.pruneDeadTransports()
	rg.mu.Unlock()

	ev := r.MuxEvents()
	require.Len(t, ev, 3)
	require.Equal(t, MuxEventLegRemoved, ev[1].Event)
	require.Equal(t, MuxEventPrimaryRehome, ev[2].Event)
	require.Equal(t, "primary leg's transport closed", ev[2].Reason)
}

func TestMuxEventRingIsBoundedAndOrdered(t *testing.T) {
	var r muxEventRing
	for i := 0; i < MuxEventRingSizeDefault+10; i++ {
		r.add(MuxEvent{Reason: fmt.Sprint(i)})
	}
	got := r.snapshot()
	require.Len(t, got, MuxEventRingSizeDefault)
	require.Equal(t, "10", got[0].Reason)
	require.Equal(t, fmt.Sprint(MuxEventRingSizeDefault+9), got[len(got)-1].Reason)
}

func TestMuxEventRingForDescFiltersAndBounds(t *testing.T) {
	var r muxEventRing
	mine := routing.NewRouteDescriptor(mustPK(t), mustPK(t), 1, 2)
	other := routing.NewRouteDescriptor(mustPK(t), mustPK(t), 3, 4)
	fields := func(d routing.RouteDescriptor) routing.RouteDescriptorFields {
		return routing.RouteDescriptorFields{DstPK: d.DstPK(), SrcPK: d.SrcPK(), DstPort: d.DstPort(), SrcPort: d.SrcPort()}
	}
	for i := 0; i < 12; i++ {
		r.add(MuxEvent{Desc: fields(mine), Reason: fmt.Sprint(i)})
		r.add(MuxEvent{Desc: fields(other), Reason: "other"})
	}
	got := r.forDesc(mine, 3)
	require.Len(t, got, 3)
	require.Equal(t, []string{"9", "10", "11"}, []string{got[0].Reason, got[1].Reason, got[2].Reason})
	require.Empty(t, r.forDesc(mine, 0))
}

// noteMuxEvent on a route group with no router (the setup node, tests) is a
// no-op rather than a nil dereference.
func TestNoteMuxEventWithoutRouterIsNoop(t *testing.T) {
	rg := NewRouteGroup(DefaultRouteGroupConfig(), routing.NewTable(logging.MustGetLogger("mux_events_rt")),
		routing.NewRouteDescriptor(mustPK(t), mustPK(t), 1, 2), nil)
	require.NotPanics(t, func() {
		rg.noteLegEvent(MuxEventLegRemoved, "no ring attached", MuxByLocal, 0, 0, nil, nil)
		rg.noteMuxEvent(MuxEvent{Event: MuxEventGroupClosed})
	})
	require.Nil(t, rg.muxEvents.snapshot())
	require.Equal(t, uuid.UUID{}, tpEntryID(nil))
}
