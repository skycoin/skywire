// Package router pkg/router/route_group_peer_park_test.go c2-net-routing
package router

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
)

func peerParkLeg(t *testing.T, rg *RouteGroup, idx int) {
	t.Helper()
	rg.mu.Lock()
	rid := rg.legs[idx].rvs.KeyRouteID()
	rg.mu.Unlock()
	require.NoError(t, rg.handleLegStatePacket(routing.MakeLegStatePacket(rid, true)))
	require.True(t, rg.legParkedByPeer(idx))
}

func peerParkCount(rg *RouteGroup) int {
	rg.peerParkMu.Lock()
	defer rg.peerParkMu.Unlock()
	return len(rg.peerParkedLegs)
}

// TestPeerParkFollowsLegAcrossPrune checks a peer-originated park stays with
// its leg when an earlier leg is pruned and the indices shift.
func TestPeerParkFollowsLegAcrossPrune(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 4)
	rg.mux.legStateEnabled = true
	peerParkLeg(t, rg, 3)

	require.Equal(t, 1, rg.pruneDeadLegs([]uuid.UUID{mts[1].Entry.ID}, "test", "test"))

	require.True(t, rg.mux.isLegStandby(2), "the parked leg moved from index 3 to 2")
	require.True(t, rg.legParkedByPeer(2), "the peer park must follow its leg to index 2")
	require.False(t, rg.legParkedByPeer(1), "the leg now at index 1 was never parked by the peer")
}

// TestPeerParkFollowsLegAcrossPrimarySwap covers the other index change.
func TestPeerParkFollowsLegAcrossPrimarySwap(t *testing.T) {
	rg, _, _ := createMuxRouteGroup(t, 3)
	rg.mux.legStateEnabled = true
	peerParkLeg(t, rg, 2)

	rg.mu.Lock()
	rg.legs[1].tp, rg.legs[2].tp = rg.legs[2].tp, rg.legs[1].tp
	rg.legs[1].fwd, rg.legs[2].fwd = rg.legs[2].fwd, rg.legs[1].fwd
	rg.legs[1].rvs, rg.legs[2].rvs = rg.legs[2].rvs, rg.legs[1].rvs
	rg.mux.swapLegs(1, 2)
	rg.mu.Unlock()

	require.True(t, rg.legParkedByPeer(1))
	require.False(t, rg.legParkedByPeer(2))
}

// TestPeerParkDroppedWithLeg checks every removal path forgets the park, so a
// leg regrown over the same transport does not inherit it.
func TestPeerParkDroppedWithLeg(t *testing.T) {
	removals := map[string]func(t *testing.T, rg *RouteGroup, idx int){
		"prune by id": func(t *testing.T, rg *RouteGroup, idx int) {
			require.Equal(t, 1, rg.pruneDeadLegs([]uuid.UUID{rg.legTransportAt(idx).Entry.ID}, "test", "test"))
		},
		"prune closed": func(t *testing.T, rg *RouteGroup, idx int) {
			rg.legTransportAt(idx).CloseForTest()
			rg.mu.Lock()
			defer rg.mu.Unlock()
			require.Len(t, rg.pruneDeadTransports(), 1)
		},
		"peer retire": func(t *testing.T, rg *RouteGroup, idx int) {
			rg.mu.Lock()
			rid := rg.legs[idx].rvs.KeyRouteID()
			rg.mu.Unlock()
			require.True(t, rg.pruneLegByConsumeRule(rid))
		},
		"operator remove": func(t *testing.T, rg *RouteGroup, idx int) {
			r := &router{rt: rg.rt, logger: rg.logger, rgsNs: map[routing.RouteDescriptor]*NoiseRouteGroup{
				rg.desc: {rg: rg, Conn: rg},
			}}
			require.NoError(t, r.RemoveMuxRouteByTransport(rg.desc, rg.legTransportAt(idx).Entry.ID))
		},
		"re-home away": func(t *testing.T, rg *RouteGroup, idx int) {
			_, err := rg.detachLegForRehome(idx, "test")
			require.NoError(t, err)
		},
	}
	for name, remove := range removals {
		t.Run(name, func(t *testing.T) {
			rg, _, _ := createMuxRouteGroup(t, 3)
			rg.mux.legStateEnabled = true
			peerParkLeg(t, rg, 2)

			remove(t, rg, 2)

			require.Zero(t, peerParkCount(rg), "the removed leg's peer park must be forgotten")
		})
	}
}
