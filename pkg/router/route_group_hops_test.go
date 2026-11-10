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
