// Package router pkg/router/scenarios_rehome_inband_emu_test.go c2-net-routing
//
// Re-home and split THROUGH AN OLD RELAY, on the emulated testbed.
//
// The fleet is heterogeneous: a relay only forwards routing.LegRehomePacket if
// its build knows the type, and live, every re-home whose leg went through an
// unpinned fleet relay timed out while the same request on a leg reaching the
// exit directly was acked every time. This rig reproduces that hop — every
// LegRehomePacket on the path is silently discarded, which is exactly what an
// old relay does with it — and requires the whole sequence to work anyway,
// because it now rides IN-BAND inside a DataPacket (mux_control_frame.go) and a
// relay forwards one of those by route ID without looking at the payload.
package router

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// oldRelayRigPair is rehomeRigPair with an old relay on the path: every
// LegRehomePacket is dropped, so nothing but the in-band frame gets through.
func oldRelayRigPair(t *testing.T, gLeg, sLeg emuLegSpec) (g, s *emuRig) {
	t.Helper()
	ml := logging.NewMasterLogger()
	ml.SetLevel(logrus.PanicLevel)
	shared := newEmuShared(ml)
	drop := []routing.PacketType{routing.LegRehomePacket}
	g = newEmuRig(t, emuOpts{
		Legs: []emuLegSpec{gLeg}, LegRehome: true, DropTypes: drop,
		SrcPort: 1, DstPort: 2, RidBase: 0, Shared: shared,
	})
	s = newEmuRig(t, emuOpts{
		Legs: []emuLegSpec{sLeg}, LegRehome: true, DropTypes: drop,
		SrcPort: 3, DstPort: 4, RidBase: 100, Shared: shared,
	})
	return g, s
}

// TestEmuRehomeAndSplitThroughARelayThatDropsTheOldPacket is the gate this
// whole change exists for: with every LegRehomePacket on the path discarded,
// the forward re-home, the split back out and the re-take must all still
// complete, and the group must carry data over the moved chain each time.
func TestEmuRehomeAndSplitThroughARelayThatDropsTheOldPacket(t *testing.T) {
	gLeg := symmetric("active-2MBs-120ms", 2*emuMB, 120*time.Millisecond, 2*emuMB)
	sLeg := symmetric("standby-2MBs-150ms", 2*emuMB, 150*time.Millisecond, 2*emuMB)
	g, s := oldRelayRigPair(t, gLeg, sLeg)
	g.A.rg.SetAppName("skysocks-client")
	g.A.rg.SetTunnelRole(tunnelRoleActive)

	// A raw LegRehomePacket cannot reach the far end on this path at all — the
	// premise of the scenario, asserted rather than assumed.
	require.True(t, g.dropped(routing.LegRehomePacket))

	// Every message of the sequence below must be counted as an in-band frame:
	// that counter rising is the proof the chain moved on the one carrier a
	// relay forwards blind, not on the type this path discards.
	inBandBefore := globalMuxCounters.legControlsInBand.Load()

	// FORWARD: the standby tunnel's chain becomes g's second leg, acked in-band.
	require.NoError(t, rehomeChain(g.A.rg, s.A.rg),
		"a re-home through a relay that drops the old packet type must still be acked")
	ga, gb := g.AddedLegs()
	require.Equal(t, [2]int{2, 2}, [2]int{ga, gb}, "the chain must be a leg of the active group on both ends")
	tpID := legTpID(t, g.A.rg, 1)

	require.Eventually(t, func() bool {
		a, b := s.AddedLegs()
		return a == 0 && b == 0 && s.A.rg.isClosed() && s.B.rg.isClosed()
	}, 5*time.Second, 20*time.Millisecond, "the consumed standby group must close on both ends")

	warm := g.Transfer(emuDown, 2*emuMB, emuTimeout)
	require.True(t, warm.HashOK, "the group must carry over the adopted chain: got %d/%d", warm.Got, warm.Bytes)

	// SPLIT: the chain goes back out to the pool, also acked in-band.
	g.A.rg.releaseLegByTransport(poolTakenLeg{tpID: tpID, from: 4},
		"no load; the leg is released back to the pool")
	var nsA, nsB *RouteGroup
	require.Eventually(t, func() bool {
		nsA = g.A.host.singleLegGroupOn(tpID)
		nsB = g.B.host.singleLegGroupOn(tpID)
		return nsA != nil && nsB != nil
	}, 5*time.Second, 20*time.Millisecond,
		"a split through a relay that drops the old packet type must still be acked")
	ga, gb = g.AddedLegs()
	require.Equal(t, [2]int{1, 1}, [2]int{ga, gb}, "the active group must be back to its own leg")
	require.Equal(t, tpID, legTpID(t, nsA, 0), "the split must keep the transport, not dial a new one")

	x := g.Transfer(emuDown, 2*emuMB, emuTimeout)
	require.True(t, x.HashOK, "the active group must still carry after the split: got %d/%d", x.Got, x.Bytes)

	// ROUND TRIP: and the chain is takeable again.
	require.NoError(t, rehomeChain(g.A.rg, nsA), "the split-out chain must be re-homable again in-band")
	ga, gb = g.AddedLegs()
	require.Equal(t, [2]int{2, 2}, [2]int{ga, gb}, "the re-taken chain must be a leg of the active group again")
	require.Equal(t, tpID, legTpID(t, g.A.rg, 1), "the re-taken leg rides the same transport it always did")

	y := g.Transfer(emuDown, 2*emuMB, emuTimeout)
	require.True(t, y.HashOK, "the group must carry over the re-taken chain: got %d/%d", y.Got, y.Bytes)

	require.GreaterOrEqual(t, globalMuxCounters.legControlsInBand.Load()-inBandBefore, uint64(6),
		"each of the three moves must have carried its request and its ack in-band")
}
