// Package router pkg/router/leg_hops_match_test.go
//
// leg.hops_match: a packet-level group only takes pool chains of its own
// length. The rig of 2026-09-24 showed the failure it stops — a two-hop tunnel
// that took a direct standby transport as its second leg.
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

func TestPoolCandidatesMatchTheGroupsHopCount(t *testing.T) {
	r := newPoolTestRouter(t)
	exit, _ := cipher.GenerateKeyPair()
	local, _ := cipher.GenerateKeyPair()
	mid := func() cipher.PubKey { pk, _ := cipher.GenerateKeyPair(); return pk }

	active, _ := poolTunnel(t, r, exit, local, 49300, tunnelRoleActive,
		poolRoute(local, mid(), exit, uuid.New(), uuid.New()), 40, 0)

	// The direct chain is the fastest standby, so it would rank first.
	direct, _ := poolTunnel(t, r, exit, local, 49310, tunnelRoleStandby,
		[]routing.Hop{{TpID: uuid.New(), From: local, To: exit}}, 10, 0)
	chain, _ := poolTunnel(t, r, exit, local, 49311, tunnelRoleStandby,
		poolRoute(local, mid(), exit, uuid.New(), uuid.New()), 60, 0)
	pool := []*RouteGroup{direct, chain}

	require.Equal(t, 2, active.legHopsTarget())
	got := poolCandidates(active, pool, nil)
	require.Len(t, got, 1, "a direct chain must not join a two-hop group")
	require.Same(t, chain, got[0].rg)

	setBool(routersettings.LegHopsMatch, false)
	active.refreshKnobs() // the group reads a snapshot its service loop refreshes
	t.Cleanup(func() { setBool(routersettings.LegHopsMatch, true) })
	require.Equal(t, 0, active.legHopsTarget())
	got = poolCandidates(active, pool, nil)
	require.Len(t, got, 2, "leg.hops_match off takes any length")
	require.Same(t, direct, got[0].rg)

	// A promotion has no group to match: every chain stays a candidate.
	setBool(routersettings.LegHopsMatch, true)
	require.Len(t, poolCandidates(nil, pool, nil), 2)
}

// TestArbiterDialsAtTheGroupsLengthWhenThePoolHasNone: a direct tunnel whose
// pool holds only two-hop chains takes none of them, and composes by dialing a
// leg of its own length instead, once per pool.leg_interval.
func TestArbiterDialsAtTheGroupsLengthWhenThePoolHasNone(t *testing.T) {
	rig := newComposeRig(t, "skysocks-client-hops-match-test", 0)
	for i := 0; i < 3; i++ {
		mid, _ := cipher.GenerateKeyPair()
		s, _ := poolTunnel(t, rig.r, rig.exit, rig.local, routing.Port(49320+i), tunnelRoleStandby,
			poolRoute(rig.local, mid, rig.exit, uuid.New(), uuid.New()), 50, 0)
		s.SetAppName("skysocks-client-hops-match-test")
		s.SetTunnelRole(tunnelRoleStandby)
		rig.pool = append(rig.pool, s)
	}
	require.Equal(t, 1, rig.active.legHopsTarget())
	require.Empty(t, poolCandidates(rig.active, rig.pool, nil))
	require.True(t, pooledAtOtherLength(rig.active, rig.pool))

	calls := 0
	inner := rig.grow(t)
	grow := func(s *RouteGroup) error {
		calls++
		require.Nil(t, s, "the fallback names no standby: it is not taking one")
		return inner(rig.pool[0])
	}
	now := time.Now()
	poolArbiterStep(rig.active, rig.pool, now, grow)
	require.Equal(t, 1, calls)
	require.Equal(t, 2, rig.active.aliveLegCount())

	// A grow that finds nothing is still paced: the next tick inside the
	// interval does not ask again.
	rig.closeLeg(t, rig.grownTps[0])
	poolArbiterStep(rig.active, rig.pool, now.Add(time.Millisecond), grow)
	require.Equal(t, 1, calls)
}
