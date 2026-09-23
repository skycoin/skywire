// Package router pkg/router/scenarios_shape_emu_test.go c2-net-routing
//
// The SHAPE CONVERGER on the emulated testbed (docs/design/mux-shape-axis.md
// steps 3-5). A session starts at 2x2 — two active tunnels of two legs each —
// and is asked for a different shape along the stream-level/packet-level axis:
//
//	2x2 -> 4x1   pure stream level: four tunnels, no packet striping
//	2x2 -> 1x4   pure packet level: one tunnel, four legs
//	4x1 -> 2x2   and back again
//
// The claim every one of them makes is the same, and it is the whole point of
// the axis: the move costs NOTHING. No transport is closed, none is dialed,
// the chain budget C = Σ n_i + |pool| is the number it started at, and the
// same download arrives intact either side of the change.
package router

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
)

// shapeEmuApp names the app the shape knob is read for. A name of its own
// keeps a per-app override in one test out of every other test's way.
const shapeEmuApp = "skysocks-client-shape-test"

// allGroups is every group this end has registered, including the ones a split
// created after the rigs were built.
func (h *emuHost) allGroups() []*RouteGroup {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*RouteGroup, 0, len(h.groups))
	for _, rg := range h.groups {
		out = append(out, rg)
	}
	return out
}

// shapeEmuRig builds one shared-dispatch session: `actives` groups the app
// marks active and `standby` pooled ones, every group over its own emulated
// leg, all between the same two visors.
func shapeEmuRig(t *testing.T, actives, standby int) []*emuRig {
	t.Helper()
	ml := logging.NewMasterLogger()
	ml.SetLevel(logrus.PanicLevel)
	shared := newEmuShared(ml)
	rigs := make([]*emuRig, 0, actives+standby)
	for i := 0; i < actives+standby; i++ {
		rtt := time.Duration(120+5*i) * time.Millisecond
		r := newEmuRig(t, emuOpts{
			Legs:      []emuLegSpec{symmetric(fmt.Sprintf("shape%d-2MBs-%v", i, rtt), 2*emuMB, rtt, 2*emuMB)},
			LegRehome: true,
			SrcPort:   routing.Port(1 + 2*i), DstPort: routing.Port(2 + 2*i),
			RidBase: 100 * i, Shared: shared,
		})
		r.A.rg.SetAppName(shapeEmuApp)
		if i < actives {
			r.A.rg.SetTunnelRole(tunnelRoleActive)
		} else {
			r.A.rg.SetTunnelRole(tunnelRoleStandby)
		}
		rigs = append(rigs, r)
	}
	return rigs
}

// shapeSession0 buckets every group this end holds by the role the app stamped
// on it, the way the router's own poolArbiterTick does each tick — so a group
// a split CREATED or a promote re-labeled is in the right bucket next tick.
func shapeSession0(rigs []*emuRig) (active, pool []*RouteGroup) {
	seen := map[routing.RouteDescriptor]bool{}
	for _, r := range rigs {
		for _, rg := range r.A.host.allGroups() {
			if seen[rg.desc] || rg.isClosed() {
				continue
			}
			seen[rg.desc] = true
			switch rg.TunnelRole() {
			case tunnelRoleActive:
				active = append(active, rg)
			case tunnelRoleStandby:
				pool = append(pool, rg)
			}
		}
	}
	return active, pool
}

// shapeChainBudget is C = Σ n_i + |pool| plus the set of transports holding it.
// Conservation means BOTH come back unchanged: the same count and the same
// transport IDs, so nothing was closed and nothing was dialed.
func shapeChainBudget(rigs []*emuRig) (int, map[uuid.UUID]struct{}) {
	active, pool := shapeSession0(rigs)
	tps := map[uuid.UUID]struct{}{}
	n := 0
	for _, rg := range append(append([]*RouteGroup(nil), active...), pool...) {
		rg.mu.Lock()
		for _, tp := range rg.tps {
			if tp != nil && !tp.IsClosed() {
				tps[tp.Entry.ID] = struct{}{}
				n++
			}
		}
		rg.mu.Unlock()
	}
	return n, tps
}

// shapeOf is the session's measured shape right now.
func shapeOf(rigs []*emuRig) string {
	active, _ := shapeSession0(rigs)
	legs := make([]int, 0, len(active))
	for _, rg := range active {
		legs = append(legs, rg.aliveLegCount())
	}
	return newShape(legs).String()
}

// composeTo2x2 is the starting state every shape test shares: the first two
// tunnels each take one chain out of the pool, by re-home, with no dial.
func composeTo2x2(t *testing.T, rigs []*emuRig) {
	t.Helper()
	active, pool := shapeSession0(rigs)
	require.Len(t, active, 2, "the session starts with two active tunnels")
	require.NoError(t, rehomeChain(active[0], pool[0]), "the first tunnel takes a pooled chain")
	require.NoError(t, rehomeChain(active[1], pool[1]), "the second tunnel takes a pooled chain")
	require.Equal(t, "2x2", shapeOf(rigs), "the session must start at 2x2")
}

// driveShape sets mux.shape and runs the arbiter round the way the router's
// own loop does — a synthetic clock past pool.leg_interval each tick, so the
// converger's one-move-per-tick budget is the only thing pacing it.
func driveShape(t *testing.T, rigs []*emuRig, spec, want string) int {
	t.Helper()
	require.NoError(t, routersettings.SetApp(shapeEmuApp, routersettings.MuxShape.Name(), spec))
	now := time.Now()
	moves := 0
	for i := 0; i < 60; i++ {
		active, pool := shapeSession0(rigs)
		before := shapeOf(rigs)
		poolArbiterRound(active, pool, now, nil)
		if shapeOf(rigs) != before {
			moves++
		}
		if shapeOf(rigs) == want {
			return moves
		}
		now = now.Add(10 * time.Second)
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the session never reached %s; it is at %s", want, shapeOf(rigs))
	return moves
}

// carries asserts a download over rig still arrives intact — the shape moved,
// the data did not stop.
func carries(t *testing.T, rig *emuRig, when string) {
	t.Helper()
	x := rig.Transfer(emuDown, 2*emuMB, emuTimeout)
	require.True(t, x.HashOK, "the session must carry %s the shape change: got %d/%d", when, x.Got, x.Bytes)
}

// shapeCase is the body all three convergence tests share.
func shapeCase(t *testing.T, spec, want string) {
	t.Helper()
	t.Cleanup(func() { routersettings.ResetApp(shapeEmuApp) })
	rigs := shapeEmuRig(t, 2, 6)
	composeTo2x2(t, rigs)

	budget, tps := shapeChainBudget(rigs)
	carries(t, rigs[0], "before")

	moves := driveShape(t, rigs, spec, want)
	require.Greater(t, moves, 0, "reaching %s must take at least one move", want)

	after, afterTps := shapeChainBudget(rigs)
	require.Equal(t, budget, after, "the chain budget must be conserved: %s -> %s", "2x2", want)
	require.Equal(t, tps, afterTps, "no transport may be closed and none dialed for a shape change")

	carries(t, rigs[0], "after")
}

// TestEmuShapeConverges2x2To4x1 is the stream-level end of the axis: the two
// wide tunnels give their second chain back and the pool supplies two more
// tunnels, for four single-leg tunnels on the same four chains.
func TestEmuShapeConverges2x2To4x1(t *testing.T) { shapeCase(t, "4x1", "4x1") }

// TestEmuShapeConverges2x2To1x4 is the packet-level end: one tunnel is
// decomposed and parked, and the survivor composes the freed chains into a
// four-leg mux.
func TestEmuShapeConverges2x2To1x4(t *testing.T) { shapeCase(t, "1x4", "1x4") }

// TestEmuShapeRoundTripsBackTo2x2 is the requirement in one line: the session
// must move along the axis and BACK, trivially, on the chains it already has.
func TestEmuShapeRoundTripsBackTo2x2(t *testing.T) {
	t.Cleanup(func() { routersettings.ResetApp(shapeEmuApp) })
	rigs := shapeEmuRig(t, 2, 6)
	composeTo2x2(t, rigs)

	budget, tps := shapeChainBudget(rigs)
	carries(t, rigs[0], "before")

	driveShape(t, rigs, "4x1", "4x1")
	mid, midTps := shapeChainBudget(rigs)
	require.Equal(t, budget, mid, "the outbound leg of the round trip must conserve the chain budget")
	require.Equal(t, tps, midTps, "and close nothing")
	carries(t, rigs[0], "at 4x1 across")

	driveShape(t, rigs, "2x2", "2x2")
	back, backTps := shapeChainBudget(rigs)
	require.Equal(t, budget, back, "the return leg must conserve it too")
	require.Equal(t, tps, backTps, "and still close nothing")
	carries(t, rigs[0], "after")
}

// rigForTp is the rig whose emulated leg carries transport tp.
func rigForTp(t *testing.T, rigs []*emuRig, tp uuid.UUID) *emuRig {
	t.Helper()
	for _, r := range rigs {
		for _, id := range r.ids {
			if id == tp {
				return r
			}
		}
	}
	t.Fatalf("no rig holds transport %s", tp)
	return nil
}

// rigForGroup is the rig whose initiator group is rg.
func rigForGroup(t *testing.T, rigs []*emuRig, rg *RouteGroup) *emuRig {
	t.Helper()
	for _, r := range rigs {
		if r.A.rg == rg {
			return r
		}
	}
	t.Fatalf("no rig owns group %s", rg.desc.String())
	return nil
}

// TestEmuShapeBacksOffALegTheExitNeverAcks is the 2026-09-23 live stall, in a
// test. One leg's split request is black-holed, so the exit never acks it. The
// converger must:
//
//	a) leave that CHAIN alone for shape.retry_backoff instead of asking it
//	   again every tick (which is what turned a 55 s download into 527 s: each
//	   attempt quiesces the leg for the whole ack timeout),
//	b) reach the target over the tunnel's OTHER leg,
//	c) make no attempt at all while the tunnel is carrying, and
//	d) record the deferral exactly once — one Info line, not one per tick.
func TestEmuShapeBacksOffALegTheExitNeverAcks(t *testing.T) {
	t.Cleanup(func() { routersettings.ResetApp(shapeEmuApp) })
	// A short ack timeout so a dead split costs the tick 300ms and not 5s, and
	// a backoff far longer than the whole drive below, so "it was not retried"
	// is a fact about the ledger and not about how fast the test ran.
	require.NoError(t, routersettings.SetValue(routersettings.LegRehomeAckTimeout.Name(), int64(300*time.Millisecond)))
	require.NoError(t, routersettings.SetValue(routersettings.ShapeRetryBackoff.Name(), int64(30*time.Minute)))
	t.Cleanup(func() {
		require.NoError(t, routersettings.SetValue(routersettings.LegRehomeAckTimeout.Name(), int64(5*time.Second)))
		require.NoError(t, routersettings.SetValue(routersettings.ShapeRetryBackoff.Name(), int64(30*time.Second)))
	})

	rigs := shapeEmuRig(t, 2, 6)
	composeTo2x2(t, rigs)

	// One more chain into the first tunnel: the session sits at 3,2, so the
	// 2x2 target needs exactly one leg back off that tunnel and nothing else.
	active, pool := shapeSession0(rigs)
	wide := active[0]
	// composeTo2x2 leaves the chains it took behind as empty standby groups,
	// and the bucket order is a map's: take a standby that still HOLDS one.
	var spare *RouteGroup
	for _, s := range pool {
		if s.aliveLegCount() == 1 {
			spare = s
			break
		}
	}
	require.NotNil(t, spare, "the pool must still hold a takeable chain")
	require.NoError(t, rehomeChain(wide, spare), "the wide tunnel takes a third chain")
	require.Equal(t, "3,2", shapeOf(rigs), "the session must start over-wide on one tunnel")
	wideRig := rigForGroup(t, rigs, wide)
	budget, tps := shapeChainBudget(rigs)

	// Black-hole the leg the converger WOULD pick, toward the exit: the split
	// request leaves and is never seen. That is the fleet symptom exactly.
	pick, _, ok := splitCandidate(wide, time.Now())
	require.True(t, ok, "the over-wide tunnel must have a splittable leg")
	t.Cleanup(func() { shapeRetries.ok(pick.tp) })
	rigForTp(t, rigs, pick.tp).Leg(0).CutUp()

	// (c) While the tunnel is CARRYING, no attempt is made at all: splitLeg
	// quiesces its leg before it tells the exit, so an attempt per tick is the
	// tunnel dropping a chain out of its stripe set for the whole ack timeout,
	// five seconds out of every five. The control is a transfer with no shape
	// target set; the deferred one must not be dramatically slower.
	control := time.Now()
	carries(t, wideRig, "before")
	controlFor := time.Since(control)

	require.NoError(t, routersettings.SetApp(shapeEmuApp, routersettings.MuxShape.Name(), "2x2"))
	busy := time.Now()
	wide.mu.Lock()
	wide.poolLoadAt = busy
	wide.mu.Unlock()
	before := MuxCountersSnapshot()
	a, p := shapeSession0(rigs)
	poolArbiterRound(a, p, busy, nil)
	require.Equal(t, before.LegSplitsSent, MuxCountersSnapshot().LegSplitsSent,
		"a tunnel that is carrying must not have a leg parked for a split's ack wait (I7)")
	for i := 0; i < wide.legCount(); i++ {
		require.False(t, wide.mux.isLegStandby(i), "leg %d must still be striping while the move is deferred", i)
	}
	loaded := time.Now()
	carries(t, wideRig, "while the move is deferred")
	require.Less(t, time.Since(loaded), 3*controlFor+2*time.Second,
		"the deferred move must not cost the tunnel its throughput: %v against a %v control", time.Since(loaded), controlFor)

	// (a) + (b) Idle again, the converger tries the dead leg once, backs off,
	// and reaches the target over the other one.
	sent := MuxCountersSnapshot()
	driveShape(t, rigs, "2x2", "2x2")
	after := MuxCountersSnapshot()

	require.Equal(t, uint64(1), shapeRetries.fails(pick.tp),
		"(d) the unacknowledged chain must be recorded, and deferred, exactly ONCE")
	require.Equal(t, sent.LegSplitsFailed+1, after.LegSplitsFailed,
		"exactly one split may go unanswered: a retry inside the backoff is the stall")
	require.Equal(t, sent.LegSplitsAcked+1, after.LegSplitsAcked, "and the other leg's split must be acked")
	require.True(t, shapeRetries.backedOff(pick.tp, time.Now()), "the dead chain must still be inside its backoff")

	// The dead chain is left exactly where it was, and nothing was closed.
	stillThere := false
	for _, l := range wide.shapeLegs() {
		stillThere = stillThere || l.tp == pick.tp
	}
	require.True(t, stillThere, "a chain that could not be handed back stays where it was (I4)")
	end, endTps := shapeChainBudget(rigs)
	require.Equal(t, budget, end, "the chain budget must be conserved across a deferred move")
	require.Equal(t, tps, endTps, "and nothing may be closed or dialed for one")
}
