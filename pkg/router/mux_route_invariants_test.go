// Package router pkg/router/mux_route_invariants_test.go
package router

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// pathHops builds a forward hop chain visiting the given ordered visor PKs
// (src, i1, i2, ..., dst). Each hop's transport ID is left zero — the mux
// invariant helpers under test key on PKs only.
func pathHops(pks ...cipher.PubKey) []routing.Hop {
	if len(pks) < 2 {
		return nil
	}
	hops := make([]routing.Hop, 0, len(pks)-1)
	for i := 0; i < len(pks)-1; i++ {
		hops = append(hops, routing.Hop{From: pks[i], To: pks[i+1]})
	}
	return hops
}

func TestRouteIntermediates(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	x, _ := cipher.GenerateKeyPair()
	y, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()

	// Direct (0-intermediate) leg has no intermediates.
	require.Empty(t, routeIntermediates(pathHops(src, dst), src, dst))

	// Two-hop leg src->x->dst yields [x].
	got := routeIntermediates(pathHops(src, x, dst), src, dst)
	require.Equal(t, []cipher.PubKey{x}, got)

	// Three-hop leg src->x->y->dst yields [x, y].
	got = routeIntermediates(pathHops(src, x, y, dst), src, dst)
	require.ElementsMatch(t, []cipher.PubKey{x, y}, got)

	// src and dst are never counted as intermediates even if a From lists them.
	require.NotContains(t, routeIntermediates(pathHops(src, x, dst), src, dst), src)
	require.NotContains(t, routeIntermediates(pathHops(src, x, dst), src, dst), dst)
}

func TestHasLoop(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	x, _ := cipher.GenerateKeyPair()
	y, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()

	require.False(t, hasLoop(nil))
	require.False(t, hasLoop(pathHops(src, dst)))
	require.False(t, hasLoop(pathHops(src, x, y, dst)))

	// Loop: src->x->src->dst passes through src twice.
	require.True(t, hasLoop(pathHops(src, x, src, dst)))
	// Loop: an intermediate repeats (src->x->y->x->dst).
	require.True(t, hasLoop(pathHops(src, x, y, x, dst)))
	// Loop: the destination appears mid-path (src->dst->y->dst).
	require.True(t, hasLoop(pathHops(src, dst, y, dst)))
}

func TestDisjointFrom(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	c, _ := cipher.GenerateKeyPair()

	// Empty candidate (direct leg) is always disjoint.
	require.True(t, disjointFrom(nil, []cipher.PubKey{a, b}))
	// Empty used set accepts anything.
	require.True(t, disjointFrom([]cipher.PubKey{a}, nil))
	// No overlap.
	require.True(t, disjointFrom([]cipher.PubKey{a}, []cipher.PubKey{b, c}))
	// Overlap on a.
	require.False(t, disjointFrom([]cipher.PubKey{a, c}, []cipher.PubKey{a, b}))
}

func TestValidMuxLeg(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	x, _ := cipher.GenerateKeyPair() // used by an existing leg
	y, _ := cipher.GenerateKeyPair() // free
	z, _ := cipher.GenerateKeyPair() // free

	// Existing legs already route through x (both directions).
	usedFwd := []cipher.PubKey{x}
	usedRev := []cipher.PubKey{x}

	fwdVia := func(mid cipher.PubKey) []routing.Hop { return pathHops(src, mid, dst) }
	revVia := func(mid cipher.PubKey) []routing.Hop { return pathHops(dst, mid, src) }

	// Disjoint candidate (via y fwd, z rev) accepted.
	require.True(t, validMuxLeg(fwdVia(y), revVia(z), src, dst, usedFwd, usedRev))

	// Overlapping-intermediate candidate rejected — forward reuses x.
	require.False(t, validMuxLeg(fwdVia(x), revVia(z), src, dst, usedFwd, usedRev))
	// Overlapping on the reverse direction is rejected too.
	require.False(t, validMuxLeg(fwdVia(y), revVia(x), src, dst, usedFwd, usedRev))

	// Looping candidate rejected even when it would otherwise be disjoint.
	loopFwd := pathHops(src, y, z, y, dst) // y appears twice
	require.False(t, validMuxLeg(loopFwd, revVia(z), src, dst, usedFwd, usedRev))

	// Direct (0-intermediate) leg is always accepted, regardless of used set.
	require.True(t, validMuxLeg(pathHops(src, dst), pathHops(dst, src), src, dst, usedFwd, usedRev))
}
