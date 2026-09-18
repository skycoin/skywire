//go:build !tinygo || (js && wasm)

package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	rs "github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
)

// restoreDialKnobs snapshots every dial knob and restores it when the test
// ends, so a test that moves one cannot leak into the next.
func restoreDialKnobs(t *testing.T) {
	t.Helper()
	unknownLat := DialUnknownLatencyCostMs()
	unknownHop := DialUnknownHopPenaltyMs()
	typeScale := DialTypePriorScale()
	tputScale := DialThroughputPriorScale()
	cands := DialRouteCandidates()
	headroom := DialMuxRouteHeadroom()
	foreground := DialForegroundMux()
	tunnelLegs := DialTunnelLegs()
	youngAge := DeadRouteYoungAge()
	planTTL := WarmPlanTTL()
	bucketCap := WarmPlanBucketCap()
	prefer := DialPreferPKs()
	t.Cleanup(func() {
		SetDialUnknownLatencyCostMs(unknownLat)
		SetDialUnknownHopPenaltyMs(unknownHop)
		SetDialTypePriorScale(typeScale)
		SetDialThroughputPriorScale(tputScale)
		SetDialRouteCandidates(cands)
		SetDialMuxRouteHeadroom(headroom)
		SetDialForegroundMux(foreground)
		SetDialTunnelLegs(tunnelLegs)
		SetDeadRouteYoungAge(youngAge)
		SetWarmPlanTTL(planTTL)
		SetWarmPlanBucketCap(bucketCap)
		SetDialPreferPKs(prefer)
	})
}

// The whole contract of settings_dial.go: every default IS the constant the
// package compiled with, so an untouched visor behaves as it did.
func TestDialKnobDefaultsMatchConstants(t *testing.T) {
	require.Equal(t, 1000.0, DialUnknownLatencyCostMs())
	require.Equal(t, float64(unknownHopPenaltyMs), DialUnknownHopPenaltyMs())
	require.Equal(t, 1.0, DialTypePriorScale())
	require.Equal(t, 1.0, DialThroughputPriorScale())
	require.Equal(t, baseRouteCandidates, DialRouteCandidates())
	require.Equal(t, muxRouteHeadroom, DialMuxRouteHeadroom())
	require.Equal(t, initialForegroundMux, DialForegroundMux())
	require.Equal(t, 0, DialTunnelLegs())
	require.Equal(t, deadRouteYoungAge, DeadRouteYoungAge())
	require.Equal(t, defaultWarmPlanTTL, WarmPlanTTL())
	require.Equal(t, warmPlanBucketCap, WarmPlanBucketCap())
	require.Empty(t, DialPreferPKs())
}

func TestDialKnobValidation(t *testing.T) {
	restoreDialKnobs(t)

	require.False(t, SetDialRouteCandidates(0), "a dial must ask for at least one candidate")
	require.False(t, SetDialMuxRouteHeadroom(-1))
	require.True(t, SetDialMuxRouteHeadroom(0), "0 headroom means 'exactly the mux degree'")
	require.False(t, SetDialForegroundMux(0))
	require.False(t, SetDeadRouteYoungAge(0))
	require.False(t, SetWarmPlanTTL(-time.Second))
	require.False(t, SetWarmPlanBucketCap(0))
	require.False(t, SetDialTunnelLegs(-2), "-1 is the lowest meaningful tunnel-leg value")
	require.True(t, SetDialTunnelLegs(-1))
	require.True(t, SetDialTypePriorScale(0), "0 drops the type prior from the score")
	require.False(t, SetDialTypePriorScale(-1))
}

// findRouteNum reads both candidate knobs on every dial.
func TestFindRouteNumFollowsKnobs(t *testing.T) {
	restoreDialKnobs(t)

	require.Equal(t, uint16(0), findRouteNum(0), "mux off lets the finder use its own default")
	require.Equal(t, uint16(baseRouteCandidates), findRouteNum(1), "floored at the historical default")
	require.Equal(t, uint16(2+muxRouteHeadroom), findRouteNum(2))

	require.True(t, SetDialRouteCandidates(6))
	require.True(t, SetDialMuxRouteHeadroom(4))
	require.Equal(t, uint16(6), findRouteNum(1), "the new floor applies")
	require.Equal(t, uint16(9), findRouteNum(5), "mux + the new headroom")
}

// The priors are multipliers, and 0 removes the term entirely.
func TestTransportCostScales(t *testing.T) {
	restoreDialKnobs(t)

	require.Equal(t, 400.0, transportCostMs("dmsg", 0), "type prior at scale 1")
	require.Equal(t, 250.0, transportCostMs("stcpr", 60_000), "measured band at scale 1")

	require.True(t, SetDialTypePriorScale(0))
	require.Equal(t, 0.0, transportCostMs("dmsg", 0), "scale 0 drops the type prior")
	require.Equal(t, 250.0, transportCostMs("stcpr", 60_000), "the throughput band is a separate knob")

	require.True(t, SetDialTypePriorScale(2))
	require.Equal(t, 800.0, transportCostMs("dmsg", 0))
	require.True(t, SetDialThroughputPriorScale(0.5))
	require.Equal(t, 125.0, transportCostMs("stcpr", 60_000))
}

func TestPathLatencyScoreUnknownCostIsLive(t *testing.T) {
	restoreDialKnobs(t)

	path := []routing.Hop{{TpID: uuid.New()}}
	require.Equal(t, 1000.0, pathLatencyScore(path, nil, nil, nil))
	require.True(t, SetDialUnknownLatencyCostMs(50))
	require.Equal(t, 50.0, pathLatencyScore(path, nil, nil, nil))
}

// pathPrefersPK matches a preferred peer anywhere but the final hop — the exit
// is on every candidate and so separates nothing.
func TestPathPrefersPK(t *testing.T) {
	restoreDialKnobs(t)

	src := cipher.PubKey{1}
	mid := cipher.PubKey{2}
	exit := cipher.PubKey{3}
	other := cipher.PubKey{4}
	path := []routing.Hop{{From: src, To: mid}, {From: mid, To: exit}}

	require.False(t, pathPrefersPK(path), "no list, no preference")

	SetDialPreferPKs([]cipher.PubKey{mid})
	require.True(t, pathPrefersPK(path))

	SetDialPreferPKs([]cipher.PubKey{other})
	require.False(t, pathPrefersPK(path))

	SetDialPreferPKs([]cipher.PubKey{exit})
	require.False(t, pathPrefersPK(path), "the destination is on every candidate")

	SetDialPreferPKs(nil)
	require.False(t, pathPrefersPK(path), "cleared")
}

// The prefer list outranks carrier class AND latency, which is the point: a
// route that won a measured set is taken even when a faster-looking one exists.
func TestRankByPathLatencyHonorsPreferList(t *testing.T) {
	restoreDialKnobs(t)

	fastTp, slowTp := uuid.New(), uuid.New()
	preferred := cipher.PubKey{9}
	exit := cipher.PubKey{7}

	fast := []routing.Hop{{TpID: fastTp, From: cipher.PubKey{1}, To: exit}}
	slow := []routing.Hop{
		{TpID: slowTp, From: cipher.PubKey{1}, To: preferred},
		{TpID: uuid.New(), From: preferred, To: exit},
	}
	latencyFor := func(id uuid.UUID) float64 {
		switch id {
		case fastTp:
			return 5
		case slowTp:
			return 400
		}
		return 400
	}

	ranked := rankByPathLatency([][]routing.Hop{fast, slow}, latencyFor)
	require.Equal(t, fastTp, ranked[0][0].TpID, "without a prefer list the fast path wins")

	SetDialPreferPKs([]cipher.PubKey{preferred})
	ranked = rankByPathLatency([][]routing.Hop{fast, slow}, latencyFor)
	require.Equal(t, slowTp, ranked[0][0].TpID, "the preferred route is taken first")
	require.Len(t, ranked, 2, "preference is an ORDERING, never a filter")
}

// The dial-time tunnel width: off by default, and it only touches a dial that
// asked for a single-route group.
func TestApplyDialTunnelLegs(t *testing.T) {
	restoreDialKnobs(t)

	require.Zero(t, applyDialTunnelLegs(nil))

	opts := &DialOptions{AppName: "skysocks-client", MuxRoutes: 1}
	require.Zero(t, applyDialTunnelLegs(opts), "off by default")
	require.Equal(t, 1, opts.MuxRoutes)

	require.True(t, SetDialTunnelLegs(4))
	require.Equal(t, 4, applyDialTunnelLegs(opts))
	require.Equal(t, 4, opts.MuxRoutes)

	// A dial that named its own mux degree keeps it.
	own := &DialOptions{AppName: "skysocks-client", MuxRoutes: 2}
	require.Zero(t, applyDialTunnelLegs(own))
	require.Equal(t, 2, own.MuxRoutes)

	// --direct is a 1-hop control route; a datagram dial has no mux fan-out;
	// a dial with no app is not a tunnel.
	direct := &DialOptions{AppName: "skysocks-client", MuxRoutes: 1, EnsureDirectTransport: true}
	require.Zero(t, applyDialTunnelLegs(direct))
	require.Equal(t, 1, direct.MuxRoutes)

	dgram := &DialOptions{AppName: "skysocks-client", MuxRoutes: 1, Datagram: true}
	require.Zero(t, applyDialTunnelLegs(dgram))

	anon := &DialOptions{MuxRoutes: 1}
	require.Zero(t, applyDialTunnelLegs(anon))

	// An asymmetric request is the caller's own shape; leave it alone.
	asym := &DialOptions{AppName: "a", MuxRoutes: 1, ReverseMuxRoutes: 3}
	require.Zero(t, applyDialTunnelLegs(asym))
}

// The warm pool follows the live TTL when it was built without a pinned one,
// and keeps a pinned TTL when it was given one (so tests stay deterministic).
func TestWarmPoolTTLFollowsKnob(t *testing.T) {
	restoreDialKnobs(t)

	live := newWarmRoutePool(0)
	require.Equal(t, WarmPlanTTL(), live.planTTL())
	require.True(t, SetWarmPlanTTL(3*time.Second))
	require.Equal(t, 3*time.Second, live.planTTL())

	pinned := newWarmRoutePool(time.Minute)
	require.Equal(t, time.Minute, pinned.planTTL(), "an explicit TTL is not overridden")
}

// The dead-route cache re-reads the young-death window on every death.
func TestDeadRouteYoungAgeIsLive(t *testing.T) {
	restoreDialKnobs(t)

	c := newDeadRouteCache(0, 0)
	path := []routing.Hop{{TpID: uuid.New(), From: cipher.PubKey{1}, To: cipher.PubKey{2}}}
	now := time.Now()

	require.False(t, c.mark(path, deadRouteYoungAge+time.Second, now), "an old death is a normal teardown")
	require.True(t, SetDeadRouteYoungAge(deadRouteYoungAge+2*time.Second))
	require.True(t, c.mark(path, deadRouteYoungAge+time.Second, now), "the widened window now counts it")
}

// The dial-time knobs are entries in the SAME catalog as the live-group ones:
// settable by catalog key, reset by the catalog's reset, and so carried by the
// one persistence map rather than a second mechanism.
func TestDialKnobsAreCatalogEntries(t *testing.T) {
	restoreDialKnobs(t)
	t.Cleanup(rs.Reset)

	for _, name := range []string{
		"dial.unknown_latency_cost_ms", "dial.unknown_hop_penalty_ms",
		"dial.type_prior_scale", "dial.throughput_prior_scale",
		"dial.candidates", "dial.candidate_headroom", "dial.foreground_mux",
		"dial.tunnel_legs", "route.dead_young_age", "warm.plan_ttl",
		"warm.plan_bucket_cap",
	} {
		require.NotNil(t, rs.Lookup(name), name)
	}

	require.NoError(t, rs.Set("dial.candidates", "7"))
	require.Equal(t, 7, DialRouteCandidates())
	require.NoError(t, rs.Set("warm.plan_ttl", "45s"))
	require.Equal(t, 45*time.Second, WarmPlanTTL())
	require.NoError(t, rs.Set("dial.tunnel_legs", "-1"), "-1 is a legal signed count")
	require.Equal(t, -1, DialTunnelLegs())
	require.Error(t, rs.Set("dial.tunnel_legs", "-2"))
	require.NoError(t, rs.Set("dial.type_prior_scale", "0"), "0 drops the term")
	require.Equal(t, 0.0, DialTypePriorScale())

	rs.Reset()
	require.Equal(t, baseRouteCandidates, DialRouteCandidates())
	require.Equal(t, defaultWarmPlanTTL, WarmPlanTTL())
}

// dial.tunnel_legs is per-app scopeable: a dial knows whose tunnel it is, so a
// subject client and its paired reference can be dialed at different widths on
// one visor.
func TestDialTunnelLegsPerApp(t *testing.T) {
	restoreDialKnobs(t)
	t.Cleanup(rs.Reset)

	require.NoError(t, rs.Set("dial.tunnel_legs", "2"))
	require.NoError(t, rs.SetApp("skysocks-client", "dial.tunnel_legs", "5"))

	require.Equal(t, 2, dialTunnelLegsFor(""), "the visor-wide value")
	require.Equal(t, 2, dialTunnelLegsFor("someone-else"), "an app with no override follows it")
	require.Equal(t, 5, dialTunnelLegsFor("skysocks-client"))

	opts := &DialOptions{MuxRoutes: 1, AppName: "skysocks-client"}
	require.Equal(t, 5, applyDialTunnelLegs(opts))
	require.Equal(t, 5, opts.MuxRoutes)

	other := &DialOptions{MuxRoutes: 1, AppName: "someone-else"}
	require.Equal(t, 2, applyDialTunnelLegs(other))
	require.Equal(t, 2, other.MuxRoutes)
}
