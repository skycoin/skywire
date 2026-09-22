// Package router pkg/router/pool_role_test.go
//
// The three gates the rig run of 2026-09-22 asked for (bench/2026-09-22,
// compose set T2xL2: 32 groups, 37 leg_added, 13 leg_removed, compose 50 MB up
// at 0.26x and the exit +100 MiB of RSS):
//
//   - a group of a ROLE-REPORTING app whose role has not landed yet is not
//     widened by anything — the pool's own tunnels were born role-less and the
//     self-heal top-up grew them before the label arrived;
//   - a tunnel's KEEPALIVE traffic is not load, so the arbiter's reverse-heavy
//     episode cannot latch on the pool's audition pings and spend a tunnel
//     every pool.leg_interval for the whole session;
//   - two pooled tunnels that hold the SAME hop path offer ONE plan, not two.
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
)

// TestRoleLessGroupOfARoleReportingAppIsNotWidened is gate one. The app has
// labelled at least one tunnel, so "no role" on another of its groups means
// the label is in flight — and a group in that state must not be widened by
// the self-heal top-up, by SetSelfHeal's width, or by the arbiter.
func TestRoleLessGroupOfARoleReportingAppIsNotWidened(t *testing.T) {
	const app = "skysocks-client-rolefix-test"
	r := newPoolTestRouter(t)
	exit, _ := cipher.GenerateKeyPair()
	local, _ := cipher.GenerateKeyPair()

	// The app labels one tunnel: from here on it is a role-reporting app.
	labelled, _ := poolTunnel(t, r, exit, local, 49180, tunnelRoleActive,
		[]routing.Hop{{TpID: uuid.New(), From: local, To: exit}}, 40, 0)
	labelled.SetAppName(app)
	labelled.SetTunnelRole(tunnelRoleActive)
	require.True(t, appReportsTunnelRoles(app), "an app that stamped a role must be registered as role-reporting")

	// A second group of the SAME app, dialed but not yet labelled.
	unlabelled, _ := poolTunnel(t, r, exit, local, 49181, "",
		[]routing.Hop{{TpID: uuid.New(), From: local, To: exit}}, 40, 0)
	unlabelled.SetAppName(app)

	require.True(t, unlabelled.tunnelRoleUnknown(), "a role-reporting app's unlabelled group has an unknown role")
	require.False(t, unlabelled.poolWideningAllowed(), "a group whose role is unknown must not be widened")

	widened := 0
	unlabelled.SetSelfHeal(func([]string) { widened++ }, 3)
	require.Equal(t, 1, unlabelled.muxWidthTarget(), "the width offered to a role-unknown group is clamped to one leg")
	unlabelled.maybeSelfHeal()
	require.Zero(t, widened, "the self-heal top-up must never widen a role-unknown group")

	// The arbiter does not touch it either: it is neither active nor standby.
	poolArbiterStep(unlabelled, []*RouteGroup{labelled}, time.Now(), func(*RouteGroup) error {
		t.Fatal("the arbiter must not grow a group whose role is unknown")
		return nil
	})

	// A group of an app that never reports roles keeps today's behaviour.
	plain, _ := poolTunnel(t, r, exit, local, 49182, "",
		[]routing.Hop{{TpID: uuid.New(), From: local, To: exit}}, 40, 0)
	plain.SetAppName("an-app-that-never-labels-anything")
	require.True(t, plain.poolWideningAllowed(), "an app that never reports roles is unaffected")

	// And the label arriving restores the width.
	unlabelled.SetTunnelRole(tunnelRoleActive)
	require.True(t, unlabelled.poolWideningAllowed(), "an active tunnel widens once its label lands")
}

// TestPoolLoadSignalIgnoresKeepaliveTraffic is gate two, and the fix for the 37
// takes. The old signal latched on ANY byte delta, so a group that was only
// keeping itself alive read as "carrying continuously" forever.
func TestPoolLoadSignalIgnoresKeepaliveTraffic(t *testing.T) {
	r := newPoolTestRouter(t)
	exit, _ := cipher.GenerateKeyPair()
	local, _ := cipher.GenerateKeyPair()
	rg, _ := poolTunnel(t, r, exit, local, 49190, tunnelRoleActive,
		[]routing.Hop{{TpID: uuid.New(), From: local, To: exit}}, 40, 0)

	floor := float64(rg.mux.knInt(routersettings.PoolLoadMinBps))
	engage := rg.mux.knDur(routersettings.UnidirFanoutEngage)
	require.Positive(t, floor, "pool.load_min_bps must have a default floor")
	now := time.Now()

	// The first sample only establishes the baseline: a group's lifetime byte
	// total is not a rate, and counting it as one latched the very first tick.
	var moved uint64 = 1 << 30
	require.False(t, rg.noteLoadSample(moved, now, floor, engage),
		"the first sample must establish the baseline, not latch the episode")

	// A keepalive trickle, sampled every second for well past the engage
	// window. It moves bytes every tick and must never read as load.
	for i := 0; i < 20; i++ {
		now = now.Add(time.Second)
		moved += 512 // a heartbeat's worth
		require.False(t, rg.noteLoadSample(moved, now, floor, engage),
			"keepalive traffic must never latch the arbiter's load episode (tick %d)", i)
	}
	// And the whole signal agrees: an idle group's mux reports no movement at
	// all, so neither source of load is engaged.
	reason, loaded := rg.poolLoadSignal(now.Add(time.Second))
	require.False(t, loaded, "an idle group must not read as loaded: %q", reason)

	// A real transfer, at the floor, for longer than the engage window.
	sustained := false
	for elapsed := time.Duration(0); elapsed <= engage+2*time.Second; elapsed += time.Second {
		now = now.Add(time.Second)
		moved += uint64(floor)
		sustained = rg.noteLoadSample(moved, now, floor, engage)
	}
	require.True(t, sustained, "a sustained transfer at pool.load_min_bps must latch the episode")
}

// TestDistinctRoutePlansKeepsOneTunnelPerRoute is gate three: two pooled
// tunnels holding the same hop path are one route, and only the better-ranked
// of them is offered — unless pool.allow_duplicate_route says otherwise, and
// then the plan names what it duplicates.
func TestDistinctRoutePlansKeepsOneTunnelPerRoute(t *testing.T) {
	local, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()
	exit, _ := cipher.GenerateKeyPair()
	tp1, tp2, other := uuid.New(), uuid.New(), uuid.New()

	shared := poolRoute(local, mid, exit, tp1, tp2)
	distinct := poolRoute(local, mid, exit, other, tp2)
	plans := []poolLegPlan{
		{fwd: shared, port: 49205, latencyMS: 55},
		{fwd: shared, port: 49235, latencyMS: 70},
		{fwd: distinct, port: 49240, latencyMS: 90},
	}

	kept := distinctRoutePlans(append([]poolLegPlan(nil), plans...), false)
	require.Len(t, kept, 2, "the two tunnels on one hop path must offer one plan between them")
	require.Equal(t, routing.Port(49205), kept[0].port, "the better-ranked holder of a shared route is the one kept")
	require.Equal(t, routing.Port(49240), kept[1].port, "a distinct route is always offered")
	for _, p := range kept {
		require.Zero(t, p.dupOf, "a plan offered by default is never a duplicate")
	}

	// The knob on: the duplicate is offered and says what it duplicates.
	all := distinctRoutePlans(append([]poolLegPlan(nil), plans...), true)
	require.Len(t, all, 3, "pool.allow_duplicate_route offers the repeat too")
	require.Equal(t, routing.Port(49205), all[1].dupOf, "the repeat must name the tunnel whose route it copies")
	require.Contains(t, all[1].source(), "duplicate of the route :49205",
		"the dial-decision reason must name the duplicate")
}

// TestHopPathSigIsTheTransportSequence pins what "the same route" means.
func TestHopPathSigIsTheTransportSequence(t *testing.T) {
	local, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()
	exit, _ := cipher.GenerateKeyPair()
	tp1, tp2 := uuid.New(), uuid.New()

	require.Equal(t, hopPathSig(poolRoute(local, mid, exit, tp1, tp2)),
		hopPathSig(poolRoute(local, mid, exit, tp1, tp2)))
	require.NotEqual(t, hopPathSig(poolRoute(local, mid, exit, tp1, tp2)),
		hopPathSig(poolRoute(local, mid, exit, tp2, tp1)),
		"hop ORDER is part of the route")
	require.Empty(t, hopPathSig(nil))
}
