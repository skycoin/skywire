// Package router pkg/router/scenarios_legsplit_emu_test.go c2-net-routing
//
// The LEG SPLIT gate, on the emulated testbed: the reverse of a re-home. An
// active group holding a chain it took from the standby pool gives it back —
// the leg leaves the group and becomes a standalone standby group on the same
// transport, with no close and no dial — and can then be taken again.
//
// The rigs share one routing table and one dispatcher per end (emuShared), so
// a frame reaches whichever group owns its consume rule at the moment it
// lands. That is what makes the rewrite real rather than bookkeeping.
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router/routersettings"
)

// legTpID is the transport ID of leg idx.
func legTpID(t *testing.T, rg *RouteGroup, idx int) uuid.UUID {
	t.Helper()
	rg.mu.Lock()
	defer rg.mu.Unlock()
	require.Greater(t, len(rg.tps), idx, "the group has no leg %d", idx)
	require.NotNil(t, rg.tps[idx])
	return rg.tps[idx].Entry.ID
}

// TestEmuSplitReleasedLegBackIntoTheStandbyPool is the gate: the pool arbiter's
// release must hand the chain BACK to the pool as a standby group of its own —
// same transport, same route IDs, re-keyed consume rule at each edge — and that
// group must be a valid pool candidate again, re-homable forward with no dial.
func TestEmuSplitReleasedLegBackIntoTheStandbyPool(t *testing.T) {
	gLeg := symmetric("active-2MBs-120ms", 2*emuMB, 120*time.Millisecond, 2*emuMB)
	sLeg := symmetric("standby-2MBs-150ms", 2*emuMB, 150*time.Millisecond, 2*emuMB)
	g, s := rehomeRigPair(t, gLeg, sLeg, true)
	g.A.rg.SetAppName("skysocks-client")
	g.A.rg.SetTunnelRole(tunnelRoleActive)

	// The take: the standby tunnel's chain becomes g's second leg.
	require.NoError(t, rehomeChain(g.A.rg, s.A.rg), "re-home of the standby chain")
	ga, gb := g.AddedLegs()
	require.Equal(t, [2]int{2, 2}, [2]int{ga, gb}, "the chain must be a leg of the active group on both ends")
	tpID := legTpID(t, g.A.rg, 1)

	// The release: no load, so the arbiter gives the leg back. It must SPLIT,
	// not close.
	g.A.rg.releaseLegByTransport(poolTakenLeg{tpID: tpID, from: 4}, "no load; the leg is released back to the pool")

	var nsA, nsB *RouteGroup
	require.Eventually(t, func() bool {
		nsA = g.A.host.singleLegGroupOn(tpID)
		nsB = g.B.host.singleLegGroupOn(tpID)
		return nsA != nil && nsB != nil
	}, 5*time.Second, 20*time.Millisecond, "the released chain must become a standby group at each edge")

	ga, gb = g.AddedLegs()
	require.Equal(t, [2]int{1, 1}, [2]int{ga, gb}, "the active group must be back to its own leg")
	require.False(t, nsA.isClosed(), "the split-out group must be alive")
	require.Equal(t, tunnelRoleStandby, nsA.TunnelRole(), "a chain handed back to the pool is a standby tunnel")
	require.Equal(t, "skysocks-client", nsA.AppName(), "the standby group belongs to the app whose pool it returns to")
	require.Equal(t, tpID, legTpID(t, nsA, 0), "the split must keep the transport, not dial a new one")
	require.Equal(t, tpID, legTpID(t, nsB, 0), "the exit's own group must hold the same transport")
	require.NotEqual(t, g.A.rg.desc, nsA.desc, "the standby group has ports of its own")

	// The remaining leg still carries: a split is not a cut.
	x := g.Transfer(emuDown, 2*emuMB, emuTimeout)
	require.True(t, x.HashOK, "the active group must still carry after the split: got %d/%d", x.Got, x.Bytes)

	// And the chain is a pool candidate again — round trip, no dial.
	require.NoError(t, rehomeChain(g.A.rg, nsA), "the split-out standby chain must be re-homable again")
	ga, gb = g.AddedLegs()
	require.Equal(t, [2]int{2, 2}, [2]int{ga, gb}, "the re-taken chain must be a leg of the active group again")
	require.Equal(t, tpID, legTpID(t, g.A.rg, 1), "the re-taken leg rides the same transport it always did")

	y := g.Transfer(emuDown, 2*emuMB, emuTimeout)
	require.True(t, y.HashOK, "the group must carry over the re-taken chain: got %d/%d", y.Got, y.Bytes)
}

// TestEmuReleasedLegClosesWithoutTheCapability is the fallback: against a peer
// that never negotiated CapLegRehome the release must close the transport
// exactly as it did before splits existed, never leave the leg in limbo.
func TestEmuReleasedLegClosesWithoutTheCapability(t *testing.T) {
	rig := newEmuRig(t, emuOpts{Legs: []emuLegSpec{
		symmetric("leg-a", 2*emuMB, 120*time.Millisecond, 2*emuMB),
		symmetric("leg-b", 2*emuMB, 150*time.Millisecond, 2*emuMB),
	}, LegRehome: false})

	tp := rig.A.tps[1]
	rig.A.rg.releaseLegByTransport(poolTakenLeg{tpID: tp.Entry.ID, from: 4}, "no load")

	require.True(t, tp.IsClosed(), "a leg that cannot be split must have its transport closed")
	require.Eventually(t, func() bool { a, _ := rig.AddedLegs(); return a == 1 }, 5*time.Second, 20*time.Millisecond,
		"the released leg must be pruned")
}

// TestEmuReleasedLegClosesWhenTheExitNeverAcks is the other half of the
// fallback: the capability is negotiated but the request never gets an answer
// (a fleet intermediate that drops the frame, a wedged exit). The split must
// time out, leave the leg where it was, and the release must then close it.
func TestEmuReleasedLegClosesWhenTheExitNeverAcks(t *testing.T) {
	require.NoError(t, routersettings.SetValue(routersettings.LegRehomeAckTimeout.Name(), int64(300*time.Millisecond)))
	t.Cleanup(func() {
		require.NoError(t, routersettings.SetValue(routersettings.LegRehomeAckTimeout.Name(), int64(5*time.Second)))
	})

	ml := logging.NewMasterLogger()
	ml.SetLevel(logrus.PanicLevel)
	rig := newEmuRig(t, emuOpts{Legs: []emuLegSpec{
		symmetric("leg-a", 2*emuMB, 120*time.Millisecond, 2*emuMB),
		symmetric("leg-b", 2*emuMB, 150*time.Millisecond, 2*emuMB),
	}, LegRehome: true, Shared: newEmuShared(ml)})
	rig.A.rg.SetTunnelRole(tunnelRoleActive)

	// Black-hole the leg toward the exit: the split request leaves and is never
	// seen, which is exactly the 2026-09-23 fleet symptom.
	rig.Leg(1).CutUp()

	tp := rig.A.tps[1]
	before := MuxCountersSnapshot()
	rig.A.rg.releaseLegByTransport(poolTakenLeg{tpID: tp.Entry.ID, from: 4}, "no load")
	after := MuxCountersSnapshot()

	require.Greater(t, after.LegSplitsSent, before.LegSplitsSent, "the split request must have been sent")
	require.Greater(t, after.LegSplitsFailed, before.LegSplitsFailed, "an unanswered split must count as failed")
	require.Equal(t, before.LegSplitsAcked, after.LegSplitsAcked, "nothing acked it")
	require.True(t, tp.IsClosed(), "the unanswered split must fall back to closing the transport")
	require.Nil(t, rig.A.host.singleLegGroupOn(tp.Entry.ID), "no half-built standby group may be left behind")
}
