// pkg/router/warm_route_pool_test.go
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// warmTwoHop builds a src->mid->dst forward plan (one intermediate = mid) whose
// first hop rides transport firstTp.
func warmTwoHop(src, mid, dst cipher.PubKey, firstTp, secondTp uuid.UUID) []routing.Hop {
	return []routing.Hop{
		{TpID: firstTp, From: src, To: mid},
		{TpID: secondTp, From: mid, To: dst},
	}
}

func TestWarmRoutePool_HitAfterPut(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	tp1, tp2 := uuid.New(), uuid.New()

	p := newWarmRoutePool(time.Minute)
	fwd := warmTwoHop(src, mid, dst, tp1, tp2)
	rev := warmTwoHop(dst, mid, src, tp2, tp1)
	p.put(dst, 2, fwd, rev)

	// A second group dialing the same exit with no excludes gets the cached plan.
	gotF, gotR, ok := p.bestPlan(dst, 2, nil, nil)
	require.True(t, ok)
	require.Equal(t, tp1, gotF[0].TpID)
	require.Len(t, gotR, 2)

	s := p.stats()
	require.Equal(t, 1, s.Exits)
	require.Equal(t, 1, s.Plans)
	require.Equal(t, uint64(1), s.Hits)
}

func TestWarmRoutePool_MissOnEmpty(t *testing.T) {
	dst, _ := cipher.GenerateKeyPair()
	p := newWarmRoutePool(time.Minute)
	_, _, ok := p.bestPlan(dst, 2, nil, nil)
	require.False(t, ok)
	require.Equal(t, uint64(1), p.stats().Misses)
}

func TestWarmRoutePool_MinHopsIsPartOfKey(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	tp1, tp2 := uuid.New(), uuid.New()

	p := newWarmRoutePool(time.Minute)
	p.put(dst, 1, warmTwoHop(src, mid, dst, tp1, tp2), nil)

	// A min-hops=2 dial must NOT be served the min-hops=1 plan.
	_, _, ok := p.bestPlan(dst, 2, nil, nil)
	require.False(t, ok)
	_, _, ok = p.bestPlan(dst, 1, nil, nil)
	require.True(t, ok)
}

func TestWarmRoutePool_ExcludeTransport(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	tp1, tp2 := uuid.New(), uuid.New()

	p := newWarmRoutePool(time.Minute)
	p.put(dst, 2, warmTwoHop(src, mid, dst, tp1, tp2), nil)

	// The only cached plan rides tp1; a caller already using tp1 as a leg must
	// get a miss (the pool must not hand back a leg the group already has).
	_, _, ok := p.bestPlan(dst, 2, []uuid.UUID{tp1}, nil)
	require.False(t, ok)
}

func TestWarmRoutePool_ExcludeIntermediate(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	midA, _ := cipher.GenerateKeyPair()
	midB, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	tpA1, tpA2 := uuid.New(), uuid.New()
	tpB1, tpB2 := uuid.New(), uuid.New()

	p := newWarmRoutePool(time.Minute)
	p.put(dst, 2, warmTwoHop(src, midA, dst, tpA1, tpA2), nil)
	p.put(dst, 2, warmTwoHop(src, midB, dst, tpB1, tpB2), nil)

	// A group whose existing leg already goes through midA must be handed the
	// midB plan (first-hop-disjoint growth from cache).
	gotF, _, ok := p.bestPlan(dst, 2, []uuid.UUID{tpA1}, []cipher.PubKey{midA})
	require.True(t, ok)
	require.Equal(t, tpB1, gotF[0].TpID)
	require.Equal(t, midB, gotF[0].To)

	// Exclude BOTH intermediates -> no disjoint plan left -> miss.
	_, _, ok = p.bestPlan(dst, 2, nil, []cipher.PubKey{midA, midB})
	require.False(t, ok)
}

func TestWarmRoutePool_TTLExpiry(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	tp1, tp2 := uuid.New(), uuid.New()

	now := time.Unix(1_000_000, 0)
	p := newWarmRoutePool(30 * time.Second)
	p.now = func() time.Time { return now }
	p.put(dst, 2, warmTwoHop(src, mid, dst, tp1, tp2), nil)

	_, _, ok := p.bestPlan(dst, 2, nil, nil)
	require.True(t, ok)

	now = now.Add(31 * time.Second) // past TTL
	_, _, ok = p.bestPlan(dst, 2, nil, nil)
	require.False(t, ok, "expired plan must not be served")
	require.Equal(t, 0, p.stats().Exits, "expired bucket evicted on read")
}

func TestWarmRoutePool_DedupByFirstHop(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	tp1, tp2, tp2b := uuid.New(), uuid.New(), uuid.New()

	p := newWarmRoutePool(time.Minute)
	p.put(dst, 2, warmTwoHop(src, mid, dst, tp1, tp2), nil)
	p.put(dst, 2, warmTwoHop(src, mid, dst, tp1, tp2b), nil) // same first hop tp1

	require.Equal(t, 1, p.stats().Plans, "same first-hop transport must dedup")
}

func TestWarmRoutePool_Invalidate(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	mid, _ := cipher.GenerateKeyPair()
	dstA, _ := cipher.GenerateKeyPair()
	dstB, _ := cipher.GenerateKeyPair()
	tp1, tp2 := uuid.New(), uuid.New()

	p := newWarmRoutePool(time.Minute)
	p.put(dstA, 2, warmTwoHop(src, mid, dstA, tp1, tp2), nil)
	p.put(dstB, 2, warmTwoHop(src, mid, dstB, tp1, tp2), nil)

	p.invalidate(dstA)
	_, _, ok := p.bestPlan(dstA, 2, nil, nil)
	require.False(t, ok)
	_, _, ok = p.bestPlan(dstB, 2, nil, nil)
	require.True(t, ok, "invalidate(dstA) must not touch dstB")

	p.invalidateAll()
	require.Equal(t, 0, p.stats().Exits)
}

func TestWarmRoutePool_NilSafe(t *testing.T) {
	var p *warmRoutePool
	require.NotPanics(t, func() {
		p.put(cipher.PubKey{}, 1, nil, nil)
		_, _, ok := p.bestPlan(cipher.PubKey{}, 1, nil, nil)
		require.False(t, ok)
		p.invalidate(cipher.PubKey{})
		p.invalidateAll()
		_ = p.stats()
	})
}
