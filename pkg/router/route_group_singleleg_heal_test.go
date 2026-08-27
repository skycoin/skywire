package router

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// TestSoleLegExcludeHops verifies the replacement dial is steered off the current
// leg's intermediate PKs (so the finder cannot re-pick the same black-holing path).
func TestSoleLegExcludeHops(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 1)
	src := rg.desc.SrcPK()
	dst := rg.desc.DstPK()
	mid, _ := cipher.GenerateKeyPair()

	rg.SetForwardHops([]routing.Hop{
		{TpID: mts[0].Entry.ID, From: src, To: mid},
		{TpID: uuid.New(), From: mid, To: dst},
	})

	rg.mu.Lock()
	excl := rg.soleLegExcludeHopsLocked()
	rg.mu.Unlock()
	require.Equal(t, []string{mid.String()}, excl, "exclude the sole leg's intermediate, not the destination")
}

// TestLegLivenessSwapsSoleBlackHoledLeg is the regression for the terminal wedge:
// a route group degraded to a SINGLE black-holing leg (classically a deceptively-
// low-latency LAN short-circuit that self-heal's target<=1 bail never replaces)
// must dial a disjoint replacement and swap the dead leg out — never sit at one
// dead leg delivering zero forever. Before the fix legLivenessServiceFn returned
// early for <2 legs, so the sole leg was never even probed.
func TestLegLivenessSwapsSoleBlackHoledLeg(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 1)
	deadID := mts[0].Entry.ID
	src := rg.desc.SrcPK()
	dst := rg.desc.DstPK()
	mid, _ := cipher.GenerateKeyPair()

	rg.SetForwardHops([]routing.Hop{
		{TpID: deadID, From: src, To: mid},
		{TpID: uuid.New(), From: mid, To: dst},
	})

	var mu sync.Mutex
	var gotExcl []string
	var addCalls int
	var replID uuid.UUID

	// Model a live disjoint replacement leg coming up on each self-heal add.
	rg.selfHealAdd = func(excludeHops []string) {
		tpID := uuid.New()
		fwd := routing.ForwardRule(DefaultRouteKeepAlive, routing.RouteID(500), routing.RouteID(600), tpID, dst, src, 1, 2) //nolint:gosec
		rvs := routing.ConsumeRule(DefaultRouteKeepAlive, routing.RouteID(600), src, dst, 2, 1)                             //nolint:gosec
		rg.rt.SaveRule(fwd)                                                                                                 //nolint:errcheck,gosec
		rg.rt.SaveRule(rvs)                                                                                                 //nolint:errcheck,gosec
		conn := newWorkingTransport()
		mt := transport.NewManagedTransportForTest(conn)
		mt.Entry = transport.Entry{ID: tpID, Type: "test"}

		rg.mu.Lock()
		rg.tps = append(rg.tps, mt)
		rg.fwd = append(rg.fwd, fwd)
		rg.rvs = append(rg.rvs, rvs)
		rg.mux.growLegs(len(rg.tps))
		rg.mux.markLegReady(len(rg.tps) - 1)
		rg.mux.rebuildWeights(rg.tps)
		rg.mu.Unlock()

		mu.Lock()
		addCalls++
		gotExcl = excludeHops
		replID = tpID
		mu.Unlock()
	}

	// The sole leg never echoes; after legPongMissThreshold cycles it is declared
	// black-holing and the swap fires. Keep the replacement (once present) echoing
	// so it is not itself re-declared dead.
	for i := 0; i < legPongMissThreshold+3; i++ {
		mu.Lock()
		r := replID
		mu.Unlock()
		if r != (uuid.UUID{}) {
			rg.legLivenessMu.Lock()
			rg.legPongSeen[r] = true
			rg.legLivenessMu.Unlock()
		}
		rg.legLivenessServiceFn(legLivenessInterval)
	}

	require.Eventually(t, func() bool {
		rg.mu.Lock()
		defer rg.mu.Unlock()
		return len(rg.tps) == 1 && rg.tps[0].Entry.ID != deadID
	}, 2*time.Second, 20*time.Millisecond,
		"the sole black-holing leg must be swapped for a live replacement, never left at zero throughput")

	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, addCalls, 1, "a replacement dial must have been attempted")
	require.Equal(t, []string{mid.String()}, gotExcl, "the replacement must exclude the dead leg's intermediate")
}

// TestLegLivenessSoleLegKeepsAliveWhenEchoing verifies the new single-leg probing
// does NOT churn a healthy lone leg: a sole leg that keeps echoing is never
// swapped, and no replacement is dialed.
func TestLegLivenessSoleLegKeepsAliveWhenEchoing(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 1)
	keep := mts[0].Entry.ID

	var addCalls int
	rg.selfHealAdd = func(_ []string) { addCalls++ }

	for i := 0; i < legPongMissThreshold+3; i++ {
		rg.legLivenessMu.Lock()
		rg.legPongSeen[keep] = true
		rg.legLivenessMu.Unlock()
		rg.legLivenessServiceFn(legLivenessInterval)
	}

	rg.mu.Lock()
	defer rg.mu.Unlock()
	require.Equal(t, 1, len(rg.tps))
	require.Equal(t, keep, rg.tps[0].Entry.ID, "a healthy lone leg must never be swapped")
	require.Equal(t, 0, addCalls, "no replacement dial for a healthy lone leg")
}
