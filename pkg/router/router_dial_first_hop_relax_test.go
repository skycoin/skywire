// router_dial_first_hop_relax_test.go: the RELAXED half of the first-hop
// admission test. Past setup.first_hop_filter_max held first hops,
// freeFirstHops stops refusing a reused first hop and starts offering it — on
// the stated condition that the reused hop still leads somewhere new. These pin
// both halves of that condition: the held count is of DISTINCT hops, and a
// reused hop is only offered WITH a distinct intermediate.
//
// The live failure these describe (campaign rig, 2026-09-18): a 32-tunnel
// standby pool reporting "32 first-hop peer(s)" while running over 4 distinct
// transports, 29 tunnels sharing one 1-hop direct route to the exit.
package router

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// The exclusion lists carry one entry per sibling route group per transport, so
// a pool collapsed onto a single first hop repeats that hop once per tunnel.
// Counting entries would let those duplicates vouch for a diversity that does
// not exist.
func TestDistinctPKs_CountsHopsNotEntries(t *testing.T) {
	a, b := mustPK(t), mustPK(t)

	require.Equal(t, 0, distinctPKs(nil))
	require.Equal(t, 1, distinctPKs([]cipher.PubKey{a, a, a, a}))
	require.Equal(t, 2, distinctPKs([]cipher.PubKey{a, b, a, b, a}))
}

// The collapse regression. One held first hop repeated 29 times must NOT unlatch
// a filter whose threshold is 8: the pool holds one hop, not twenty-nine, and
// the candidate over it stays refused so the fill settles instead of cloning its
// own route to the ceiling.
func TestFreeFirstHops_DuplicateHoldsDoNotUnlatchTheFilter(t *testing.T) {
	src, dst := mustPK(t), mustPK(t)

	// 29 sibling entries, all naming the same first-hop peer — the shape
	// siblingRouteGroupExclusions produces for a collapsed pool.
	held := make([]cipher.PubKey, 0, 29)
	for i := 0; i < 29; i++ {
		held = append(held, dst)
	}
	require.Greater(t, len(held), SetupFirstHopFilterMax(),
		"the list must be long enough to trip the threshold if entries were counted")

	clone := directCand(src, dst, uuid.New(), 136)
	r := &router{}
	opts := &DialOptions{
		DiversifyTransports:     true,
		RequireDisjointFirstHop: true,
		ExcludeFirstHopPeers:    held,
	}

	require.Empty(t, r.freeFirstHops([][]routing.Hop{clone}, opts),
		"29 copies of one held hop is one held hop: the reused candidate must stay refused")
	require.True(t, r.firstHopExcluded(clone, opts),
		"the pre-setup gate must agree, so the dial raises ErrNoDisjointFirstHop")
}

// Past the threshold on genuinely distinct hops, a 1-hop DIRECT candidate over a
// held first hop is still refused: it has no intermediate, so reusing its first
// hop reuses the whole path. This is the candidate that filled the live pool.
func TestFreeFirstHops_RelaxedStillRefusesTheDirectClone(t *testing.T) {
	src, dst := mustPK(t), mustPK(t)

	// Enough DISTINCT held hops to relax the filter, the exit among them.
	held := []cipher.PubKey{dst}
	for len(held) <= SetupFirstHopFilterMax() {
		held = append(held, mustPK(t))
	}

	clone := directCand(src, dst, uuid.New(), 136)
	r := &router{}
	opts := &DialOptions{
		DiversifyTransports:     true,
		RequireDisjointFirstHop: true,
		ExcludeFirstHopPeers:    held,
	}

	require.Empty(t, r.freeFirstHops([][]routing.Hop{clone}, opts),
		"a direct route adds no intermediate: reusing its first hop reuses the path")
}

// The case the relaxation exists for: a reused first hop that reaches the exit
// through an intermediate no sibling holds is a genuinely different path, and
// must be offered.
func TestFreeFirstHops_RelaxedOffersAReusedHopWithANewIntermediate(t *testing.T) {
	src, dst := mustPK(t), mustPK(t)
	reusedHop, freshMid, heldMid := mustPK(t), mustPK(t), mustPK(t)

	held := []cipher.PubKey{reusedHop}
	for len(held) <= SetupFirstHopFilterMax() {
		held = append(held, mustPK(t))
	}

	// src -> reusedHop -> freshMid -> dst: the first hop is taken, the second
	// intermediate is not.
	fresh := []routing.Hop{hop(src, reusedHop), hop(reusedHop, freshMid), hop(freshMid, dst)}
	// src -> reusedHop -> heldMid -> dst: every intermediate already held.
	stale := []routing.Hop{hop(src, reusedHop), hop(reusedHop, heldMid), hop(heldMid, dst)}

	r := &router{}
	opts := &DialOptions{
		DiversifyTransports:     true,
		RequireDisjointFirstHop: true,
		ExcludeFirstHopPeers:    held,
		ExcludeIntermediatePKs:  []cipher.PubKey{reusedHop, heldMid},
	}

	got := r.freeFirstHops([][]routing.Hop{fresh, stale}, opts)
	require.Len(t, got, 1, "only the path with an unheld intermediate qualifies")
	require.Equal(t, freshMid, got[0][1].To)
}

// pathIntermediates is what separates the two cases above: the nodes strictly
// between source and destination, which a direct route has none of.
func TestPathIntermediates(t *testing.T) {
	src, dst := mustPK(t), mustPK(t)
	a, b := mustPK(t), mustPK(t)

	require.Empty(t, pathIntermediates(nil))
	require.Empty(t, pathIntermediates([]routing.Hop{hop(src, dst)}),
		"a 1-hop direct route has no intermediate")
	require.Equal(t, []cipher.PubKey{a},
		pathIntermediates([]routing.Hop{hop(src, a), hop(a, dst)}))
	require.Equal(t, []cipher.PubKey{a, b},
		pathIntermediates([]routing.Hop{hop(src, a), hop(a, b), hop(b, dst)}))
}
