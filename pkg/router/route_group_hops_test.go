// Package router pkg/router/route_group_hops_test.go c2-net-routing
package router

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestRouteHopDetailsFallbackStartsEachLegLocally checks the fallback used when
// no forward hops are recorded. Legs are parallel, so each starts at this visor.
func TestRouteHopDetailsFallbackStartsEachLegLocally(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 3)
	for _, mt := range mts {
		pk, _ := cipher.GenerateKeyPair()
		setRemoteForTest(mt, pk)
	}

	hops := rg.RouteHopDetails()

	require.Len(t, hops, 3)
	for i, h := range hops {
		require.Equal(t, rg.desc.DstPK().String(), h.From, "leg %d must start at the local visor", i)
		require.Equal(t, mts[i].Remote().String(), h.To)
	}
}

// TestAppendForwardLegHookSkipsFirstLeg checks the two append paths agree: the
// leg-change hook is for mux growth, not for a group's first leg.
func TestAppendForwardLegHookSkipsFirstLeg(t *testing.T) {
	full, mts, _ := createMuxRouteGroup(t, 2)
	rg := NewRouteGroup(DefaultRouteGroupConfig(), full.rt, full.desc, nil)
	hook := &legChangeRecorder{}
	rg.SetLegChangeHook(hook, DialInfo{})

	rg.appendForwardLeg(full.legs[0].fwd, mts[0], "test")
	require.Empty(t, hook.changes, "the first leg is not a leg change")

	rg.appendForwardLeg(full.legs[1].fwd, mts[1], "test")
	require.Equal(t, []LegChange{{Event: "added", LegIndex: 1}}, hook.changes)
}
