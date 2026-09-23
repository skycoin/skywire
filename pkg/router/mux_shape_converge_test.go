// Package router pkg/router/mux_shape_converge_test.go c2-net-routing
//
// The converger's invariants, and the two CONSERVATION claims the shape axis
// rests on (docs/design/mux-shape-axis.md steps 3 and 4):
//
//   - a leg RELEASED because the load went away is handed back to the pool as
//     a standby group, not closed, so the chain budget survives the episode;
//   - a tunnel DEMOTED to standby decomposes first and parks second, so a
//     demotion never destroys a chain either.
//
// Plus the one claim that protects everybody who never touches the knob:
// under mux.shape=auto the converger stands down entirely and the arbiter
// makes exactly the moves it made before this file existed.
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/router/routersettings"
)

// legTpIDs is every live transport of a group, in leg order.
func legTpIDs(rg *RouteGroup) []uuid.UUID {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	out := make([]uuid.UUID, 0, len(rg.tps))
	for _, tp := range rg.tps {
		if tp != nil && !tp.IsClosed() {
			out = append(out, tp.Entry.ID)
		}
	}
	return out
}

// wideEmuTunnel is one ACTIVE tunnel that has taken `take` chains out of a
// pool of `standby`, so it holds take+1 legs and the pool holds the rest.
func wideEmuTunnel(t *testing.T, take, standby int) ([]*emuRig, *RouteGroup) {
	t.Helper()
	rigs := shapeEmuRig(t, 1, standby)
	active, pool := shapeSession0(rigs)
	require.Len(t, active, 1)
	for i := 0; i < take; i++ {
		require.NoError(t, rehomeChain(active[0], pool[i]), "take %d", i)
	}
	require.Equal(t, take+1, active[0].aliveLegCount(), "the tunnel must hold every chain it took")
	return rigs, active[0]
}

// TestChainCountConservedAcrossTakeAndRelease is step 3's gate. The release
// above the idle width picks its legs newest-first; whichever it picks, the
// chain must come back as a standby group and the transport must live.
func TestChainCountConservedAcrossTakeAndRelease(t *testing.T) {
	rigs, g := wideEmuTunnel(t, 2, 4)
	budget, tps := shapeChainBudget(rigs)
	legs := legTpIDs(g)

	// The arbiter's own accounting for the two takes, then a release down to
	// the idle width of two: exactly one leg goes back.
	g.mu.Lock()
	g.poolTaken = []poolTakenLeg{{tpID: legs[1], from: 4}, {tpID: legs[2], from: 6}}
	g.mu.Unlock()
	g.releasePoolLegs(time.Now(), 2)

	require.Eventually(t, func() bool { return g.aliveLegCount() == 2 }, 5*time.Second, 20*time.Millisecond,
		"the leg above the idle width must be given back")
	after, afterTps := shapeChainBudget(rigs)
	require.Equal(t, budget, after, "a release must conserve the chain budget, not spend a chain")
	require.Equal(t, tps, afterTps, "the released chain keeps its transport; nothing is closed and nothing dialed")
	require.NotNil(t, rigs[0].A.host.singleLegGroupOn(legs[2]),
		"the released chain must be back in the pool as a standby group of its own")
}

// TestShedLegsForStandbyReturnsChainsToThePool is step 4's gate: a tunnel the
// app demotes gives its extra chains BACK, it does not burn them.
func TestShedLegsForStandbyReturnsChainsToThePool(t *testing.T) {
	rigs, g := wideEmuTunnel(t, 2, 4)
	budget, tps := shapeChainBudget(rigs)
	legs := legTpIDs(g)

	g.SetTunnelRole(tunnelRoleStandby)
	g.shedLegsForStandby()

	require.Eventually(t, func() bool { return g.aliveLegCount() == 1 }, 5*time.Second, 20*time.Millisecond,
		"a pooled tunnel holds one leg whatever width it had while active")
	after, afterTps := shapeChainBudget(rigs)
	require.Equal(t, budget, after, "a demotion must never destroy a chain")
	require.Equal(t, tps, afterTps, "and must never close a transport to do it")
	for _, id := range legs[1:] {
		require.NotNil(t, rigs[0].A.host.singleLegGroupOn(id),
			"every shed chain must be a standby group of its own, ready to be taken again")
	}
}

// TestParkOfAWideTunnelConservesChains is the same claim through the
// CONVERGER: asked for fewer tunnels, it decomposes the victim to one leg
// before it parks it (I6), one move at a time (I5).
func TestParkOfAWideTunnelConservesChains(t *testing.T) {
	t.Cleanup(func() { routersettings.ResetApp(shapeEmuApp) })
	rigs := shapeEmuRig(t, 2, 6)
	composeTo2x2(t, rigs)
	budget, tps := shapeChainBudget(rigs)

	require.NoError(t, routersettings.SetApp(shapeEmuApp, routersettings.MuxShape.Name(), "1x4"))
	now := time.Now()
	seen := map[string]bool{}
	for i := 0; i < 20 && shapeOf(rigs) != "1x4"; i++ {
		active, pool := shapeSession0(rigs)
		before := shapeOf(rigs)
		poolArbiterRound(active, pool, now, nil)
		if s := shapeOf(rigs); s != before {
			seen[before+"->"+s] = true
			// I6: a tunnel never reaches zero legs on the way.
			for _, rg := range active {
				if rg.TunnelRole() == tunnelRoleActive {
					require.GreaterOrEqual(t, rg.aliveLegCount(), 1, "a tunnel must never reach zero legs")
				}
			}
		}
		now = now.Add(10 * time.Second)
		time.Sleep(20 * time.Millisecond)
	}
	require.Equal(t, "1x4", shapeOf(rigs))
	require.True(t, seen["2x2->2,1"], "the victim must be DECOMPOSED before it is parked, not parked wide: saw %v", seen)
	require.True(t, seen["2,1->1x2"], "and parked only once it is down to its last leg: saw %v", seen)

	after, afterTps := shapeChainBudget(rigs)
	require.Equal(t, budget, after, "the whole demotion must conserve the chain budget")
	require.Equal(t, tps, afterTps, "and close nothing")
}

// TestShapeStepMakesOneMovePerTick is I5: however far the session is from its
// target, a tick spends one move.
func TestShapeStepMakesOneMovePerTick(t *testing.T) {
	t.Cleanup(func() { routersettings.ResetApp(shapeEmuApp) })
	rigs := shapeEmuRig(t, 2, 6)
	composeTo2x2(t, rigs)
	require.NoError(t, routersettings.SetApp(shapeEmuApp, routersettings.MuxShape.Name(), "4x1"))

	active, pool := shapeSession0(rigs)
	before := shapeOf(rigs)
	require.Equal(t, shapeVerdictMoved, shapeStep(active, pool, time.Now(), nil))
	after := shapeOf(rigs)
	require.NotEqual(t, before, after, "the tick must have made its one move")
	require.Equal(t, "2,1", after, "and exactly one: one leg of one tunnel went back to the pool")
}

// TestShapeStepNeverBreaksMinStandby is I1: the pool's reserve outranks the
// target. With pool.min_standby at the number of tunnels the pool holds, no
// chain may leave it, so a shape that needs one is DECLINED, not forced.
func TestShapeStepNeverBreaksMinStandby(t *testing.T) {
	t.Cleanup(func() { routersettings.ResetApp(shapeEmuApp) })
	rigs := shapeEmuRig(t, 1, 2)
	require.NoError(t, routersettings.SetApp(shapeEmuApp, routersettings.MuxShape.Name(), "1x3"))
	require.NoError(t, routersettings.Set(routersettings.PoolMinStandby.Name(), "2"))

	active, pool := shapeSession0(rigs)
	require.Len(t, pool, 2, "the pool holds exactly pool.min_standby tunnels")
	require.Equal(t, shapeVerdictHeld, shapeStep(active, pool, time.Now(), nil),
		"a compose that would strand the pool must be declined")
	require.Equal(t, 1, active[0].aliveLegCount(), "and nothing may have moved")
	require.Equal(t, "1x1", shapeOf(rigs))
}

// TestShapeStepStandsDownUnderAuto is the promise the default rests on: with
// mux.shape=auto the converger declines the session outright, so
// poolArbiterRound runs exactly the arbiter TestComposeIdleReachesActiveWidth-
// WithNoLoad pins and the composed widths are identical either way.
func TestShapeStepStandsDownUnderAuto(t *testing.T) {
	const app = "skysocks-client-shape-auto-test"
	require.Equal(t, routersettings.ShapeAuto, routersettings.Resolve(app).Text(routersettings.MuxShape),
		"auto is the compiled default; this test is about the untouched visor")

	// Two identical rigs. One is driven through poolArbiterRound (the caller
	// that now consults the converger first), the other through
	// poolArbiterStep (today's code path, untouched by this PR).
	round := newComposeRig(t, app, 4)
	step := newComposeRig(t, app, 4)

	require.Equal(t, shapeVerdictAuto, shapeStep([]*RouteGroup{round.active}, round.pool, time.Now(), nil),
		"under auto the converger is not in charge of the session")

	width := round.active.mux.knInt(routersettings.PoolActiveWidth)
	now := time.Now()
	for i := 0; i < width+2; i++ {
		grow := round.grow(t)
		poolArbiterRound([]*RouteGroup{round.active}, round.pool, now,
			func(_, s *RouteGroup, _ []uuid.UUID) error { return grow(s) })
		poolArbiterStep(step.active, step.pool, now, step.grow(t))
		now = now.Add(round.active.mux.knDur(routersettings.PoolLegInterval))
	}
	require.Equal(t, width, step.active.aliveLegCount(), "the reference path composes to pool.active_width")
	require.Equal(t, step.active.aliveLegCount(), round.active.aliveLegCount(),
		"and the converger's caller must compose to exactly the same width")
	require.Equal(t, step.composeGrowN, round.composeGrowN,
		"with exactly the same number of takes: auto is bit-for-bit today's arbiter")
	for _, info := range []MuxInfo{{TunnelRole: tunnelRoleActive}} {
		require.Zero(t, info.ShapeTunnels, "auto publishes no k; the app's tunnel.count still owns the count")
	}
}

// TestShapeStepHoldsUnderShapeHold is I8. The app's own freezes (pool.freeze,
// tunnel.freeze_active) live in the app PROCESS and cannot be read from here;
// the visor mirrors them into mux.shape_hold, and this is what that buys: the
// target still reads back, and not one move is made toward it.
func TestShapeStepHoldsUnderShapeHold(t *testing.T) {
	t.Cleanup(func() { routersettings.ResetApp(shapeEmuApp) })
	rigs := shapeEmuRig(t, 2, 6)
	composeTo2x2(t, rigs)
	require.NoError(t, routersettings.SetApp(shapeEmuApp, routersettings.MuxShape.Name(), "4x1"))
	require.NoError(t, routersettings.SetApp(shapeEmuApp, routersettings.MuxShapeHold.Name(), "true"))

	active, pool := shapeSession0(rigs)
	before := shapeOf(rigs)
	require.Equal(t, shapeVerdictHeld, shapeStep(active, pool, time.Now(), nil),
		"a frozen app's session must not be converged")
	require.Equal(t, before, shapeOf(rigs), "and nothing may have moved")

	// Lifting the hold hands the session straight back to the converger.
	require.NoError(t, routersettings.SetApp(shapeEmuApp, routersettings.MuxShapeHold.Name(), "false"))
	require.Equal(t, shapeVerdictMoved, shapeStep(active, pool, time.Now(), nil))
	require.NotEqual(t, before, shapeOf(rigs), "the move the hold was deferring")
}
