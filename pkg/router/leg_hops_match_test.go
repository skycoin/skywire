// Package router pkg/router/leg_hops_match_test.go
//
// leg.hops_match: a packet-level group only takes pool chains of its own
// length. The rig of 2026-09-24 showed the failure it stops — a two-hop tunnel
// that took a direct standby transport as its second leg.
package router

import (
	"testing"

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
