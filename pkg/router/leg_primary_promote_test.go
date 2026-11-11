package router

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
)

// TestParkedLegTakingPrimaryTellsPeer checks a parked leg that inherits the
// primary slot goes active on both ends, not only locally.
func TestParkedLegTakingPrimaryTellsPeer(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 3)
	rg.muxEvents = &muxEventRing{}
	rg.mux.legStateEnabled = true
	rg.mux.setLegStandby(1, true)
	rg.mu.Lock()
	active := routing.MakeLegStatePacket(rg.legs[1].fwd.NextRouteID(), false)
	rg.mu.Unlock()
	told := make(chan struct{}, 1)
	conns[1].onWrite = func(b []byte) {
		if bytes.Contains(b, active) {
			select {
			case told <- struct{}{}:
			default:
			}
		}
	}

	require.True(t, rg.pruneLegByConsumeRule(routing.RouteID(0+100)))

	require.Equal(t, 0, legIdxOf(rg, mts[1]), "the parked leg is the new primary")
	require.False(t, rg.mux.isLegStandby(0), "the primary is never standby")
	select {
	case <-told:
	case <-time.After(5 * time.Second):
		t.Fatal("the peer was not told the parked leg went active")
	}
	require.Equal(t, 1, countEvents(rg, MuxEventLegPromoted))
}

// TestActivePrimarySuccessorSendsNoLegState checks an already active leg that
// inherits the primary slot is not announced.
func TestActivePrimarySuccessorSendsNoLegState(t *testing.T) {
	rg, _, _ := createMuxRouteGroup(t, 3)
	rg.muxEvents = &muxEventRing{}
	rg.mux.legStateEnabled = true

	require.True(t, rg.pruneLegByConsumeRule(routing.RouteID(0+100)))

	require.Equal(t, 0, countEvents(rg, MuxEventLegPromoted))
}

// TestLegRemovalDropsStaleGrouping checks a bottleneck grouping, whose ids are
// leg indices, does not outlive a removal or a primary swap.
func TestLegRemovalDropsStaleGrouping(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 3)
	groups := func() []int {
		rg.mux.legMu.RLock()
		defer rg.mux.legMu.RUnlock()
		return rg.mux.legGroups
	}

	rg.mux.SetLegGroups([]int{0, 1, 1})
	rg.reelectPrimary(mts[2].Entry.ID)
	require.Nil(t, groups(), "a swap must drop the grouping")

	rg.mux.SetLegGroups([]int{0, 1, 1})
	require.True(t, rg.pruneLegByConsumeRule(routing.RouteID(0+100)))
	require.Nil(t, groups(), "a removal must drop the grouping")
}
