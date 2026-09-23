// Package router pkg/router/pool_compose_idle_test.go
//
// pool.compose_idle: an ACTIVE tunnel of a role-reporting app is held at
// pool.active_width legs whether or not it is carrying anything. The mux is
// here for privacy and for surviving a cut leg, and a spare that only appears
// once the load latch fires cannot carry a cut that happens before it.
//
// Three claims, one test each:
//   - an IDLE active tunnel reaches pool.active_width, and stays single-leg
//     with the knob off;
//   - a CUT leg on a composed tunnel is refilled on the next tick;
//   - the composed legs are never released, however long the tunnel is idle.
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// composeRig is one active tunnel of a role-reporting app plus enough standby
// siblings to satisfy pool.min_standby, all to the same exit.
type composeRig struct {
	r            *router
	active       *RouteGroup
	pool         []*RouteGroup
	exit, local  cipher.PubKey
	grownTps     []uuid.UUID
	nextRouteID  routing.RouteID
	composeGrowN int
}

func newComposeRig(t *testing.T, app string, standby int) *composeRig {
	t.Helper()
	rig := &composeRig{r: newPoolTestRouter(t), nextRouteID: 500}
	rig.exit, _ = cipher.GenerateKeyPair()
	rig.local, _ = cipher.GenerateKeyPair()

	rig.active, _ = poolTunnel(t, rig.r, rig.exit, rig.local, 49300,
		tunnelRoleActive, []routing.Hop{{TpID: uuid.New(), From: rig.local, To: rig.exit}}, 40, 0)
	rig.active.SetAppName(app)
	rig.active.SetTunnelRole(tunnelRoleActive)
	rig.active.SetSelfHeal(nil, 1) // one leg dialed, as a proxy tunnel is

	for i := 0; i < standby; i++ {
		s, _ := poolTunnel(t, rig.r, rig.exit, rig.local, routing.Port(49310+i),
			tunnelRoleStandby, []routing.Hop{{TpID: uuid.New(), From: rig.local, To: rig.exit}}, float64(50+i), 0)
		s.SetAppName(app)
		s.SetTunnelRole(tunnelRoleStandby)
		rig.pool = append(rig.pool, s)
	}
	return rig
}

// grow stands in for GrowMuxFromPool: the fleet's exits do not ack a re-home,
// so "dialed on the pool's plan" is the path that actually runs, and it ends
// with one more leg appended to the active group.
func (rig *composeRig) grow(t *testing.T) func(*RouteGroup) error {
	t.Helper()
	return func(s *RouteGroup) error {
		tpID := uuid.New()
		mt := transport.NewManagedTransportForTest(newWorkingTransport())
		mt.Entry = transport.Entry{ID: tpID, Type: "test"}
		rig.r.tm.InjectTransportForTest(mt)

		rid := rig.nextRouteID
		rig.nextRouteID += 2
		fwd := routing.ForwardRule(DefaultRouteKeepAlive, rid, rid+1, tpID, rig.local, rig.exit,
			rig.active.desc.DstPort(), poolTestExitPort)
		rvs := routing.ConsumeRule(DefaultRouteKeepAlive, rid+1, rig.local, rig.exit,
			poolTestExitPort, rig.active.desc.DstPort())
		require.NoError(t, rig.r.rt.SaveRule(fwd))
		require.NoError(t, rig.r.rt.SaveRule(rvs))
		rig.active.appendRules(fwd, rvs, mt, "test: leg composed from the pool")
		rig.grownTps = append(rig.grownTps, tpID)
		rig.composeGrowN++
		s.noteTunnelConsumed(rig.active.desc.DstPort())
		return nil
	}
}

// closeLeg cuts the transport of leg tpID, the way a dead first hop does.
func (rig *composeRig) closeLeg(t *testing.T, tpID uuid.UUID) {
	t.Helper()
	rig.active.mu.Lock()
	defer rig.active.mu.Unlock()
	for _, tp := range rig.active.tps {
		if tp != nil && tp.Entry.ID == tpID {
			require.NoError(t, tp.Close())
			rig.active.pruneDeadTransports()
			return
		}
	}
	t.Fatalf("leg %s is not on the active group", tpID)
}

// TestComposeIdleReachesActiveWidthWithNoLoad is the default: an idle active
// tunnel composes to pool.active_width, and with pool.compose_idle off it
// stays on the single leg it dialed.
func TestComposeIdleReachesActiveWidthWithNoLoad(t *testing.T) {
	rig := newComposeRig(t, "skysocks-client-compose-idle-test", 4)
	width := rig.active.mux.knInt(routersettings.PoolActiveWidth)
	require.Equal(t, width, rig.active.poolIdleWidth(), "an idle active tunnel's width is pool.active_width")

	now := time.Now()
	for i := 0; i < width+2 && rig.active.aliveLegCount() < width; i++ {
		poolArbiterStep(rig.active, rig.pool, now, rig.grow(t))
		now = now.Add(rig.active.mux.knDur(routersettings.PoolLegInterval))
	}
	require.Equal(t, width, rig.active.aliveLegCount(),
		"an IDLE active tunnel must be composed at pool.active_width so a cut leg fails over instantly")
	// The leg's provenance is what `skywire cli proxy mux info` and
	// `visor state --select mux` print for it (LegInfo.Source), so an
	// idle-composed leg is listed exactly like one taken under load.
	require.Contains(t, rig.active.poolLegSource(rig.grownTps[0]), "dialed on the pool's plan",
		"an idle-composed leg must name its standby source for `mux info` and `visor state`")

	// Knob off: the arbiter is a load response again.
	require.NoError(t, routersettings.Set("pool.compose_idle", "false"))
	defer func() { require.NoError(t, routersettings.Set("pool.compose_idle", "true")) }()

	off := newComposeRig(t, "skysocks-client-compose-idle-off-test", 4)
	require.Zero(t, off.active.poolIdleWidth(), "with the knob off there is no idle width")
	at := time.Now()
	for i := 0; i < 4; i++ {
		poolArbiterStep(off.active, off.pool, at, off.grow(t))
		at = at.Add(off.active.mux.knDur(routersettings.PoolLegInterval))
	}
	require.Equal(t, 1, off.active.aliveLegCount(),
		"with pool.compose_idle off an idle tunnel takes no leg at all — today's behavior")
	require.Zero(t, off.composeGrowN, "and nothing was dialed")
}

// TestComposeIdleRefillsACutLeg is the point of the whole knob: the tunnel
// keeps flowing on the leg it has left, and the arbiter puts the width back.
func TestComposeIdleRefillsACutLeg(t *testing.T) {
	rig := newComposeRig(t, "skysocks-client-compose-cut-test", 5)
	width := rig.active.mux.knInt(routersettings.PoolActiveWidth)

	now := time.Now()
	for i := 0; i < width+2 && rig.active.aliveLegCount() < width; i++ {
		poolArbiterStep(rig.active, rig.pool, now, rig.grow(t))
		now = now.Add(rig.active.mux.knDur(routersettings.PoolLegInterval))
	}
	require.Equal(t, width, rig.active.aliveLegCount())
	grown := rig.composeGrowN

	// The composed leg's transport dies.
	rig.closeLeg(t, rig.grownTps[len(rig.grownTps)-1])
	require.Equal(t, width-1, rig.active.aliveLegCount(), "the group keeps flowing on the surviving leg")
	require.False(t, rig.active.isClosed(), "a cut leg must not take the group down")

	// The next tick refills it, from the pool, on the pool's own plan.
	now = now.Add(rig.active.mux.knDur(routersettings.PoolLegInterval))
	poolArbiterStep(rig.active, rig.pool, now, rig.grow(t))
	require.Equal(t, width, rig.active.aliveLegCount(), "the arbiter must refill a cut leg to the idle width")
	require.Equal(t, grown+1, rig.composeGrowN, "exactly one replacement was dialed, not a storm")

	// The self-heal top-up is the other refill path, and it must stay out of
	// this one: the tunnel was dialed with one leg, so its selfHealTarget is 1
	// and maybeSelfHeal returns before dialing anything.
	healed := 0
	rig.active.SetSelfHeal(func([]string) { healed++ }, 1)
	rig.active.maybeSelfHeal()
	require.Zero(t, healed, "self-heal must not double-dial a leg the arbiter owns")
}

// TestComposeIdleNeverReleasesTheComposedWidth pins the release rule: legs at
// the idle width stay however long the tunnel is quiet.
func TestComposeIdleNeverReleasesTheComposedWidth(t *testing.T) {
	rig := newComposeRig(t, "skysocks-client-compose-release-test", 4)
	width := rig.active.mux.knInt(routersettings.PoolActiveWidth)

	now := time.Now()
	for i := 0; i < width+2 && rig.active.aliveLegCount() < width; i++ {
		poolArbiterStep(rig.active, rig.pool, now, rig.grow(t))
		now = now.Add(rig.active.mux.knDur(routersettings.PoolLegInterval))
	}
	require.Equal(t, width, rig.active.aliveLegCount())

	// Well past pool.leg_release, with no load at any tick.
	for i := 0; i < 10; i++ {
		now = now.Add(10 * rig.active.mux.knDur(routersettings.PoolLegRelease))
		poolArbiterStep(rig.active, rig.pool, now, rig.grow(t))
	}
	require.Equal(t, width, rig.active.aliveLegCount(),
		"an idle composed tunnel must never be released below pool.active_width")
	require.NotEmpty(t, rig.active.poolLegSource(rig.grownTps[0]),
		"the composed leg keeps its provenance: it was never released back to the pool")
}

// twoActiveRig is one (app, exit) session with TWO active tunnels sharing one
// standby pool — the shape the fleet runs with pool.compose_idle on, and the
// one the per-group first-hop rule was blind to.
type twoActiveRig struct {
	r            *router
	active, pool []*RouteGroup
	exit, local  cipher.PubKey
	nextRouteID  routing.RouteID
}

func newTwoActiveRig(t *testing.T, app string, actives, standby int) *twoActiveRig {
	t.Helper()
	rig := &twoActiveRig{r: newPoolTestRouter(t), nextRouteID: 700}
	rig.exit, _ = cipher.GenerateKeyPair()
	rig.local, _ = cipher.GenerateKeyPair()

	for i := 0; i < actives; i++ {
		g, _ := poolTunnel(t, rig.r, rig.exit, rig.local, routing.Port(49400+i), tunnelRoleActive,
			[]routing.Hop{{TpID: uuid.New(), From: rig.local, To: rig.exit}}, 40, 0)
		g.SetAppName(app)
		g.SetTunnelRole(tunnelRoleActive)
		g.SetSelfHeal(nil, 1) // one leg dialed, as a proxy tunnel is
		rig.active = append(rig.active, g)
	}
	for i := 0; i < standby; i++ {
		s, _ := poolTunnel(t, rig.r, rig.exit, rig.local, routing.Port(49420+i), tunnelRoleStandby,
			[]routing.Hop{{TpID: uuid.New(), From: rig.local, To: rig.exit}}, float64(50+i), 0)
		s.SetAppName(app)
		s.SetTunnelRole(tunnelRoleStandby)
		rig.pool = append(rig.pool, s)
	}
	return rig
}

// grow models the fallback that actually runs against the fleet — the leg is
// dialed on the POOLED tunnel's own plan, so it lands on that tunnel's first
// hop — and fails the test if the exclusion list handed to the dial still
// names a hop a sibling active tunnel holds.
func (rig *twoActiveRig) grow(t *testing.T) func(g, s *RouteGroup, exclude []uuid.UUID) error {
	t.Helper()
	return func(g, s *RouteGroup, exclude []uuid.UUID) error {
		s.mu.Lock()
		mt := s.tps[0]
		s.mu.Unlock()
		for _, id := range exclude {
			require.NotEqual(t, mt.Entry.ID, id,
				"the grow fallback picks its own plan, so the sibling's first hops must reach its dial")
		}
		rid := rig.nextRouteID
		rig.nextRouteID += 2
		fwd := routing.ForwardRule(DefaultRouteKeepAlive, rid, rid+1, mt.Entry.ID, rig.local, rig.exit,
			g.desc.DstPort(), poolTestExitPort)
		rvs := routing.ConsumeRule(DefaultRouteKeepAlive, rid+1, rig.local, rig.exit,
			poolTestExitPort, g.desc.DstPort())
		require.NoError(t, rig.r.rt.SaveRule(fwd))
		require.NoError(t, rig.r.rt.SaveRule(rvs))
		g.appendRules(fwd, rvs, mt, "test: leg composed from the pool")
		return nil
	}
}

// compose runs arbiter rounds until every active tunnel is at the width.
func (rig *twoActiveRig) compose(t *testing.T, rounds int) {
	t.Helper()
	now := time.Now()
	for i := 0; i < rounds; i++ {
		poolArbiterRound(rig.active, rig.pool, now, rig.grow(t))
		now = now.Add(rig.active[0].mux.knDur(routersettings.PoolLegInterval))
	}
}

// firstHops is the set of first-hop transports g holds.
func firstHops(g *RouteGroup) map[uuid.UUID]struct{} {
	held := make(map[uuid.UUID]struct{})
	noteHeldFirstHops(g, held)
	return held
}

// TestPoolArbiterNeverPutsTwoActiveTunnelsOnOneFirstHop is the fail-over rule
// one level out from the per-group one: with pool.compose_idle on, BOTH active
// tunnels of an app compose in the same tick, and until the held set was
// shared they were offered the same best standby and both took its first hop
// (rig 2026-09-23: both tunnels on transport 08e154d4). One transport dying
// then cut a leg in both, which is the failure the composed width exists to
// survive. pool.allow_duplicate_route is the opt-out, and keeps the old
// behavior exactly.
func TestPoolArbiterNeverPutsTwoActiveTunnelsOnOneFirstHop(t *testing.T) {
	rig := newTwoActiveRig(t, "skysocks-client-two-active-test", 2, 5)
	width := rig.active[0].mux.knInt(routersettings.PoolActiveWidth)
	require.Greater(t, width, 1, "pool.active_width must compose past the dialed leg")

	rig.compose(t, width+2)
	for i, g := range rig.active {
		require.Equal(t, width, g.aliveLegCount(), "active tunnel %d composes to pool.active_width", i)
	}
	held := firstHops(rig.active[0])
	for id := range firstHops(rig.active[1]) {
		require.NotContains(t, held, id,
			"two ACTIVE tunnels of one app must never hold legs on one first hop: the transport dying cuts a leg in both")
	}

	// The opt-in knob restores the old behavior: both tunnels are offered the
	// same best-ranked standby and both ride it.
	require.NoError(t, routersettings.Set("pool.allow_duplicate_route", "true"))
	defer func() { require.NoError(t, routersettings.Set("pool.allow_duplicate_route", "false")) }()

	dup := newTwoActiveRig(t, "skysocks-client-two-active-dup-test", 2, 5)
	dup.compose(t, width+2)
	shared := 0
	held = firstHops(dup.active[0])
	for id := range firstHops(dup.active[1]) {
		if _, ok := held[id]; ok {
			shared++
		}
	}
	require.NotZero(t, shared, "pool.allow_duplicate_route opts back in to sharing a first hop")
}
