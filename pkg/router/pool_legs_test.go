// Package router pkg/router/pool_legs_test.go
//
// Coverage for POOL-SOURCED MUX LEGS (pool_legs.go): an active tunnel's extra
// legs are built on the routes its own STANDBY sibling tunnels already hold,
// seeded into the visor-level warm-route pool so the aux-leg dial never asks
// the route finder.
//
// The four things that must hold, and each has a test below:
//   - a standby sibling's plan is picked up, ranked, and lands in the cache
//     carrying the label the dial-decision event prints;
//   - a plan whose first hop the target group ALREADY holds is skipped — the
//     binding-bottleneck rule from docs/design/shared-warm-route-pool.md;
//   - ranking is by measured route latency, with the first-hop throughput
//     prior as the tiebreak;
//   - with no standby sibling nothing is seeded and the cache is untouched, so
//     the dial path is byte-for-byte today's.
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// poolTestExitPort is the exit-side service port every tunnel of the app
// shares; the per-tunnel identity is the DST port (what `mux info` prints).
const poolTestExitPort routing.Port = 3

// poolTestApp is the app tag every group in these tests carries. A sibling with
// a different tag is another app's tunnel, not this app's pool.
const poolTestApp = "skysocks-client"

// poolTunnel installs a single-leg route group modeling one of an app's
// tunnels: registered in rgsNs under the RECEIVE-side descriptor (Src = the
// exit, Dst = this visor) the router keys groups by, tagged with app + role,
// and with its primary leg's forward route recorded so standbyPoolPlans can
// read it. hops is that route; thrBps and latencyMS are the capacity prior on
// its first hop and its measured end-to-end route latency.
func poolTunnel(t *testing.T, r *router, exit, local cipher.PubKey, dstPort routing.Port,
	role string, hops []routing.Hop, latencyMS, thrBps float64) (*RouteGroup, routing.RouteDescriptor) {
	t.Helper()
	desc := routing.NewRouteDescriptor(exit, local, poolTestExitPort, dstPort)

	tpID := hops[0].TpID
	mt := transport.NewManagedTransportForTest(newWorkingTransport())
	mt.Entry = transport.Entry{ID: tpID, Type: "test", ThroughputBps: thrBps}
	r.tm.InjectTransportForTest(mt)

	fwd := routing.ForwardRule(DefaultRouteKeepAlive, routing.RouteID(1), routing.RouteID(101), tpID, local, exit, dstPort, poolTestExitPort)
	rvs := routing.ConsumeRule(DefaultRouteKeepAlive, routing.RouteID(101), local, exit, poolTestExitPort, dstPort)
	require.NoError(t, r.rt.SaveRule(fwd))
	require.NoError(t, r.rt.SaveRule(rvs))

	rg := NewRouteGroup(DefaultRouteGroupConfig(), r.rt, desc, r.mLogger)
	rg.mux = newRouteMux(r.mLogger.PackageLogger("pool_legs_mux"), false)
	rg.initiator = true
	rg.appendRules(fwd, rvs, mt, "test: primary leg")
	rg.SetAppName(poolTestApp)
	rg.SetTunnelRole(role)
	rg.recordLegRoute(hops, reverseHops(hops))
	if latencyMS > 0 {
		rg.legLivenessMu.Lock()
		rg.legE2ELatency[tpID] = latencyMS
		rg.legLivenessMu.Unlock()
	}

	r.mx.Lock()
	r.rgsNs[desc] = &NoiseRouteGroup{rg: rg}
	r.mx.Unlock()
	return rg, desc
}

// poolRoute builds a 2-hop forward route local -> mid -> exit over tp1 then
// tp2. Two hops because an aux mux leg is the multihop kind: a 1-hop plan would
// be the direct transport the primary already rides.
func poolRoute(local, mid, exit cipher.PubKey, tp1, tp2 uuid.UUID) []routing.Hop {
	return []routing.Hop{
		{TpID: tp1, From: local, To: mid},
		{TpID: tp2, From: mid, To: exit},
	}
}

// newPoolTestRouter is newLegTestRouter with a warm-route pool pinned to a long
// TTL, so a retune of the live WarmPlanTTL knob elsewhere cannot make these
// tests flaky (the same discipline warm_route_pool_test.go uses).
func newPoolTestRouter(t *testing.T) *router {
	t.Helper()
	r := newLegTestRouter(t)
	r.warmRoutes = newWarmRoutePool(time.Hour)
	return r
}

// TestPoolLegsSeedsStandbySiblingPlan is the core behavior: the active tunnel
// has one leg and a standby sibling to the same exit holds a disjoint route.
// Seeding must place that sibling's route in the pool under the exit key, and
// the very next aux-leg lookup — the one addOneAuxLeg makes — must be served it
// with the label naming which standby tunnel it came from.
func TestPoolLegsSeedsStandbySiblingPlan(t *testing.T) {
	r := newPoolTestRouter(t)
	exit, _ := cipher.GenerateKeyPair()
	local, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()

	directTp := uuid.New()
	active, desc := poolTunnel(t, r, exit, local, 49170, "active",
		[]routing.Hop{{TpID: directTp, From: local, To: exit}}, 40, 0)

	standbyFirst, standbySecond := uuid.New(), uuid.New()
	_, _ = poolTunnel(t, r, exit, local, 49171, tunnelRoleStandby,
		poolRoute(local, mid, exit, standbyFirst, standbySecond), 55, 8e6)

	require.Equal(t, 1, r.seedPoolPlans(desc, 1), "the one standby sibling's route is the one plan to seed")

	excludeIDs, excludeRemoteIDs, excludePKs := r.groupExcludes(&NoiseRouteGroup{rg: active}, local, exit)
	fwd, rev, source, ok := r.warmRoutes.bestPlanSourced(exit, 1, excludeIDs, excludeRemoteIDs, excludePKs)
	require.True(t, ok, "the aux-leg dial must be served the standby tunnel's route, not sent to the route finder")
	require.Equal(t, standbyFirst, fwd[0].TpID, "the leg rides the standby tunnel's first hop")
	require.Equal(t, exit, fwd[len(fwd)-1].To)
	require.Equal(t, local, rev[len(rev)-1].To, "the reverse plan must land back on this visor")
	require.Equal(t, "pool plan from standby group :49171", source,
		"the dial-decision event must name which standby tunnel the plan came from")
}

// TestPoolLegsSkipsFirstHopTheGroupAlreadyHolds is the binding-bottleneck rule.
// The standby sibling is perfectly good but leaves this visor over the very
// transport the active tunnel's existing leg rides; a second leg there is one
// link's capacity wearing two route IDs, so the plan must never reach the cache.
func TestPoolLegsSkipsFirstHopTheGroupAlreadyHolds(t *testing.T) {
	r := newPoolTestRouter(t)
	exit, _ := cipher.GenerateKeyPair()
	local, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()

	sharedFirst := uuid.New()
	_, desc := poolTunnel(t, r, exit, local, 49170, "active",
		[]routing.Hop{{TpID: sharedFirst, From: local, To: exit}}, 40, 0)

	// Same first hop as the active tunnel, different second hop: disjoint
	// everywhere it does not matter.
	_, _ = poolTunnel(t, r, exit, local, 49171, tunnelRoleStandby,
		poolRoute(local, mid, exit, sharedFirst, uuid.New()), 30, 9e6)

	require.Zero(t, r.seedPoolPlans(desc, 1),
		"a plan on a first hop the group already holds must be dropped before it reaches the cache")
	require.Zero(t, r.warmRoutes.stats().Plans)
}

// TestPoolLegsRankByRouteLatencyThenThroughput pins the order the plans are
// offered in: measured end-to-end route latency first (the number a routing
// policy judges a leg by), the first hop's observed capacity as the tiebreak
// between equal latencies, and an unmeasured plan last — 0 ms means "never
// pinged", not "instant".
func TestPoolLegsRankByRouteLatencyThenThroughput(t *testing.T) {
	slow := poolLegPlan{port: 1, latencyMS: 200, throughputBps: 9e6}
	fast := poolLegPlan{port: 2, latencyMS: 50, throughputBps: 1e6}
	fastFat := poolLegPlan{port: 3, latencyMS: 50, throughputBps: 8e6}
	unmeasured := poolLegPlan{port: 4, latencyMS: 0, throughputBps: 9e6}

	plans := []poolLegPlan{unmeasured, slow, fast, fastFat}
	rankPoolPlans(plans)

	got := []routing.Port{plans[0].port, plans[1].port, plans[2].port, plans[3].port}
	require.Equal(t, []routing.Port{3, 2, 1, 4}, got,
		"lowest latency first, fatter first hop breaking the tie, never-measured last")
}

// TestPoolLegsRankingReachesTheCache is the ranking test at the seam that
// matters: with two eligible standby siblings the pool must serve the FASTER
// one to the first aux leg and the slower one only to the second.
func TestPoolLegsRankingReachesTheCache(t *testing.T) {
	r := newPoolTestRouter(t)
	exit, _ := cipher.GenerateKeyPair()
	local, _ := cipher.GenerateKeyPair()
	midA, _ := cipher.GenerateKeyPair()
	midB, _ := cipher.GenerateKeyPair()

	active, desc := poolTunnel(t, r, exit, local, 49170, "active",
		[]routing.Hop{{TpID: uuid.New(), From: local, To: exit}}, 40, 0)

	slowTp, fastTp := uuid.New(), uuid.New()
	_, _ = poolTunnel(t, r, exit, local, 49171, tunnelRoleStandby,
		poolRoute(local, midA, exit, slowTp, uuid.New()), 210, 9e6)
	_, _ = poolTunnel(t, r, exit, local, 49172, tunnelRoleStandby,
		poolRoute(local, midB, exit, fastTp, uuid.New()), 60, 2e6)

	require.Equal(t, 2, r.seedPoolPlans(desc, 1))

	nrg := &NoiseRouteGroup{rg: active}
	excludeIDs, excludeRemoteIDs, excludePKs := r.groupExcludes(nrg, local, exit)
	fwd, _, source, ok := r.warmRoutes.bestPlanSourced(exit, 1, excludeIDs, excludeRemoteIDs, excludePKs)
	require.True(t, ok)
	require.Equal(t, fastTp, fwd[0].TpID, "the lower-latency standby route must be served first")
	require.Equal(t, "pool plan from standby group :49172", source)

	// Take the fast plan's first hop and its intermediate out of play, as a
	// group that had just attached it would, and the slower sibling is next.
	excludeIDs = append(excludeIDs, fastTp)
	excludePKs = append(excludePKs, midB)
	fwd, _, source, ok = r.warmRoutes.bestPlanSourced(exit, 1, excludeIDs, excludeRemoteIDs, excludePKs)
	require.True(t, ok)
	require.Equal(t, slowTp, fwd[0].TpID)
	require.Equal(t, "pool plan from standby group :49171", source)
}

// TestPoolLegsNoStandbySiblingLeavesCacheUntouched is the degrade-to-today
// case, and the reason the seed can be unconditional: an ACTIVE sibling and
// another app's standby tunnel are both present, and neither is borrowable, so
// nothing is seeded, the cache reports a clean miss, and the caller falls
// through to the route-finder path it always used.
func TestPoolLegsNoStandbySiblingLeavesCacheUntouched(t *testing.T) {
	r := newPoolTestRouter(t)
	exit, _ := cipher.GenerateKeyPair()
	local, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()

	active, desc := poolTunnel(t, r, exit, local, 49170, "active",
		[]routing.Hop{{TpID: uuid.New(), From: local, To: exit}}, 40, 0)

	// A sibling that is carrying its own streams is not spare capacity.
	_, _ = poolTunnel(t, r, exit, local, 49171, "active",
		poolRoute(local, mid, exit, uuid.New(), uuid.New()), 60, 9e6)
	// A standby tunnel belonging to some other app is not this app's pool.
	other, _ := poolTunnel(t, r, exit, local, 49172, tunnelRoleStandby,
		poolRoute(local, mid, exit, uuid.New(), uuid.New()), 60, 9e6)
	other.SetAppName("vpn-client")

	require.Zero(t, r.seedPoolPlans(desc, 1))
	require.Zero(t, r.warmRoutes.stats().Plans, "nothing borrowable, nothing cached")

	excludeIDs, excludeRemoteIDs, excludePKs := r.groupExcludes(&NoiseRouteGroup{rg: active}, local, exit)
	_, _, _, ok := r.warmRoutes.bestPlanSourced(exit, 1, excludeIDs, excludeRemoteIDs, excludePKs)
	require.False(t, ok, "a clean miss is the signal to fall back to fetchBestRoutes, exactly as before")
}

// TestPoolLegsSkipsPlanBelowTheMinHopsFloor guards the pool's bucket key: the
// bucket is keyed by (exit, min-hops), so a 1-hop direct standby route must not
// be seeded into a min_hops=2 bucket where a multihop dial would be served it.
func TestPoolLegsSkipsPlanBelowTheMinHopsFloor(t *testing.T) {
	r := newPoolTestRouter(t)
	exit, _ := cipher.GenerateKeyPair()
	local, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()

	_, desc := poolTunnel(t, r, exit, local, 49170, "active",
		[]routing.Hop{{TpID: uuid.New(), From: local, To: exit}}, 40, 0)
	_, _ = poolTunnel(t, r, exit, local, 49171, tunnelRoleStandby,
		[]routing.Hop{{TpID: uuid.New(), From: local, To: exit}}, 30, 9e6)

	require.Zero(t, r.seedPoolPlans(desc, 2), "a 1-hop route cannot satisfy a min_hops=2 dial")

	// The same sibling, two hops long, is seeded at that floor.
	_, _ = poolTunnel(t, r, exit, local, 49172, tunnelRoleStandby,
		poolRoute(local, mid, exit, uuid.New(), uuid.New()), 30, 9e6)
	require.Equal(t, 1, r.seedPoolPlans(desc, 2))
}

// TestPoolLegsTunnelGroupByLocalPort pins the lookup both ingresses share: the
// tunnel is named by the port the app dialed from, which on a group keyed
// receive-side is the DESCRIPTOR'S DST PORT. Every tunnel of one app shares the
// exit-side src port, so matching that first could never name a single tunnel.
func TestPoolLegsTunnelGroupByLocalPort(t *testing.T) {
	r := newPoolTestRouter(t)
	exit, _ := cipher.GenerateKeyPair()
	local, _ := cipher.GenerateKeyPair()

	_, _ = poolTunnel(t, r, exit, local, 49170, "active",
		[]routing.Hop{{TpID: uuid.New(), From: local, To: exit}}, 40, 0)
	wanted, _ := poolTunnel(t, r, exit, local, 49171, tunnelRoleStandby,
		[]routing.Hop{{TpID: uuid.New(), From: local, To: exit}}, 40, 0)

	desc, nrg, ok := r.tunnelGroupByLocalPort(49171)
	require.True(t, ok)
	require.Same(t, wanted, nrg.rg)
	require.Equal(t, routing.Port(49171), desc.DstPort())

	_, _, ok = r.tunnelGroupByLocalPort(50000)
	require.False(t, ok, "an unknown port is an error, not a silent match on the shared src port")
}
