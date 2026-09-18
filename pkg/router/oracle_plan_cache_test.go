// Package router pkg/router/oracle_plan_cache_test.go
package router

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// rigShapedSets builds the measured shape of the campaign rig: the local visor
// holds transports to `local` peers, the exit to `remote`, and `shared` of those
// peers are common to both. The counts are the live ones — 349 local, 576
// remote, 274 shared — so the candidate-set computation is exercised at the size
// it actually runs at, not on a toy.
func rigShapedSets(local, remote, shared int) (src, dst cipher.PubKey, localTps []oracleLocalTp, dstEntries []*transport.Entry) {
	src, _ = cipher.GenerateKeyPair()
	dst, _ = cipher.GenerateKeyPair()

	sharedPKs := make([]cipher.PubKey, shared)
	for i := range sharedPKs {
		sharedPKs[i], _ = cipher.GenerateKeyPair()
	}

	// Local side: the shared peers plus local-only ones. Types spread the way
	// the rig's own table does — mostly sudph, some stcpr/webrtc/squicr — so the
	// type-preference tie-breaks are exercised.
	types := []tptypes.Type{tptypes.SUDPH, tptypes.STCPR, tptypes.QUIC, tptypes.WEBRTC}
	for i := 0; i < local; i++ {
		var peer cipher.PubKey
		if i < shared {
			peer = sharedPKs[i]
		} else {
			peer, _ = cipher.GenerateKeyPair()
		}
		localTps = append(localTps, oracleLocalTp{
			id:        uuid.New(),
			remotePK:  peer,
			tpType:    types[i%len(types)],
			latencyMs: float64(10 + i%200),
		})
	}

	// Destination side: the same shared peers plus remote-only ones.
	for i := 0; i < remote; i++ {
		var peer cipher.PubKey
		if i < shared {
			peer = sharedPKs[i]
		} else {
			peer, _ = cipher.GenerateKeyPair()
		}
		dstEntries = append(dstEntries, &transport.Entry{
			ID:      uuid.New(),
			Edges:   [2]cipher.PubKey{peer, dst},
			Type:    types[(i+1)%len(types)],
			Latency: float64(20 + i%150),
		})
	}
	return src, dst, localTps, dstEntries
}

// The candidate set is the INTERSECTION of the two transport tables, and it is
// the whole intersection — one leg per shared intermediate, not one leg.
func TestComputeDisjoint2HopRoutes_IntersectsTheTwoTables(t *testing.T) {
	src, dst, localTps, dstEntries := rigShapedSets(349, 576, 274)

	legs, err := computeDisjoint2HopRoutes(src, dst, localTps, dstEntries, nil, 0)
	require.NoError(t, err)
	require.Len(t, legs, 274, "every shared intermediate is a candidate")

	seen := make(map[cipher.PubKey]struct{}, len(legs))
	for _, l := range legs {
		require.NotEqual(t, src, l.Intermediate)
		require.NotEqual(t, dst, l.Intermediate)
		require.Len(t, l.Forward, 2)
		require.Len(t, l.Reverse, 2)
		require.Equal(t, src, l.Forward[0].From)
		require.Equal(t, l.Intermediate, l.Forward[0].To)
		require.Equal(t, dst, l.Forward[1].To)
		_, dup := seen[l.Intermediate]
		require.False(t, dup, "one leg per intermediate")
		seen[l.Intermediate] = struct{}{}
	}

	// A cap is honored: a caller that wants 8 of the 274 gets the best 8.
	capped, err := computeDisjoint2HopRoutes(src, dst, localTps, dstEntries, nil, 8)
	require.NoError(t, err)
	require.Len(t, capped, 8)
	for i := range capped {
		require.Equal(t, legs[i].Intermediate, capped[i].Intermediate, "the cap takes the best, in order")
	}
}

// Exclusions remove candidates; they never change the set that is computed.
func TestComputeDisjoint2HopRoutes_HonoursExclusions(t *testing.T) {
	src, dst, localTps, dstEntries := rigShapedSets(20, 20, 10)
	all, err := computeDisjoint2HopRoutes(src, dst, localTps, dstEntries, nil, 0)
	require.NoError(t, err)
	require.Len(t, all, 10)

	opts := &DialOptions{ExcludeIntermediatePKs: []cipher.PubKey{all[0].Intermediate, all[1].Intermediate}}
	rest, err := computeDisjoint2HopRoutes(src, dst, localTps, dstEntries, opts, 0)
	require.NoError(t, err)
	require.Len(t, rest, 8)
	for _, l := range rest {
		require.NotEqual(t, all[0].Intermediate, l.Intermediate)
		require.NotEqual(t, all[1].Intermediate, l.Intermediate)
	}
}

// ONE query for N concurrent callers: the fill's whole point.
func TestOraclePlanCache_SingleflightsTheQuery(t *testing.T) {
	c := newOraclePlanCache()
	src, dst, localTps, dstEntries := rigShapedSets(40, 40, 16)
	legs, err := computeDisjoint2HopRoutes(src, dst, localTps, dstEntries, nil, 0)
	require.NoError(t, err)

	var calls int32
	release := make(chan struct{})
	var mu sync.Mutex
	var fetchErr error // always nil here; the failure path has its own test
	fetch := func(context.Context) ([]twoHopLeg, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-release // hold the first caller inside the fetch so the rest pile up
		return legs, fetchErr
	}

	const n = 8
	var wg sync.WaitGroup
	got := make([][]twoHopLeg, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], errs[i] = c.legsFor(context.Background(), src, dst, fetch)
		}(i)
	}
	// Give the goroutines time to reach the cache before the fetch returns.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	require.EqualValues(t, 1, calls, "eight concurrent dials made ONE destination-transport query")
	for i := 0; i < n; i++ {
		require.NoError(t, errs[i])
		require.Len(t, got[i], 16)
	}
	s := c.stats()
	require.EqualValues(t, 1, s.Queries)
	require.EqualValues(t, n-1, s.Shared+s.Hits)
}

// A failed fetch must not be cached: the next caller retries.
func TestOraclePlanCache_DoesNotCacheAFailedQuery(t *testing.T) {
	c := newOraclePlanCache()
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()

	boom := errors.New("destination did not answer")
	_, err := c.legsFor(context.Background(), src, dst, func(context.Context) ([]twoHopLeg, error) {
		return nil, boom
	})
	require.ErrorIs(t, err, boom)

	called := false
	_, err = c.legsFor(context.Background(), src, dst, func(context.Context) ([]twoHopLeg, error) {
		called = true
		return nil, nil
	})
	require.NoError(t, err)
	require.True(t, called, "the failure was not cached")
}

// N concurrent dials take N DISTINCT intermediates, which is what they cannot
// work out from sibling route groups that do not exist yet.
func TestOraclePlanCache_ClaimsAreDistinct(t *testing.T) {
	c := newOraclePlanCache()
	src, dst, localTps, dstEntries := rigShapedSets(40, 40, 12)
	legs, err := computeDisjoint2HopRoutes(src, dst, localTps, dstEntries, nil, 0)
	require.NoError(t, err)

	const n = 8
	var wg sync.WaitGroup
	claims := make([]twoHopLeg, n)
	oks := make([]bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			claims[i], oks[i] = c.claim(src, dst, legs, nil)
		}(i)
	}
	wg.Wait()

	seen := make(map[uuid.UUID]struct{}, n)
	for i := 0; i < n; i++ {
		require.True(t, oks[i], "12 candidates, 8 dials: every one is served")
		id := claims[i].Forward[0].TpID
		_, dup := seen[id]
		require.False(t, dup, "two dials took the same first hop")
		seen[id] = struct{}{}
	}

	// Past the candidate count the claim fails and the caller falls back — never
	// an error, just "no free candidate".
	for i := 0; i < 4; i++ {
		_, ok := c.claim(src, dst, legs, nil)
		require.True(t, ok, "candidates 9..12")
	}
	_, ok := c.claim(src, dst, legs, nil)
	require.False(t, ok, "all 12 claimed")
	require.EqualValues(t, 1, c.stats().Crowded)

	// A release hands one back immediately.
	c.release(src, dst, claims[0])
	regot, ok := c.claim(src, dst, legs, nil)
	require.True(t, ok)
	require.Equal(t, claims[0].Forward[0].TpID, regot.Forward[0].TpID)
}

// The eligible filter is the caller's own exclusion, applied on top of claims.
func TestOraclePlanCache_ClaimRespectsEligibility(t *testing.T) {
	c := newOraclePlanCache()
	src, dst, localTps, dstEntries := rigShapedSets(20, 20, 6)
	legs, err := computeDisjoint2HopRoutes(src, dst, localTps, dstEntries, nil, 0)
	require.NoError(t, err)

	want := legs[3].Intermediate
	got, ok := c.claim(src, dst, legs, func(l twoHopLeg) bool { return l.Intermediate == want })
	require.True(t, ok)
	require.Equal(t, want, got.Intermediate)

	_, ok = c.claim(src, dst, legs, func(l twoHopLeg) bool { return l.Intermediate == want })
	require.False(t, ok, "the only eligible candidate is already claimed")
}
