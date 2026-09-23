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
