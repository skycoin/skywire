package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
)

// TestRemovedLegLeavesNoStateByTransport checks a leg that leaves takes its
// park time and latency estimates with it.
func TestRemovedLegLeavesNoStateByTransport(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 3)
	id := mts[1].Entry.ID
	rg.noteAdaptivePark(id, "test")
	rg.mux.setLegE2ERTT(id, 120)
	rg.mux.recordAckDelayTp(id, 30*time.Millisecond)
	_, held := rg.adaptiveParkHeld(id)
	require.True(t, held)

	require.True(t, rg.pruneLegByConsumeRule(routing.RouteID(1+100)))

	_, held = rg.adaptiveParkHeld(id)
	require.False(t, held, "a later leg on this transport must not inherit the park hold")
	require.Zero(t, rg.mux.legE2ERttMsTp(id))
	rg.mux.ackDelayByTpMu.Lock()
	_, kept := rg.mux.ackDelayByTp[id]
	rg.mux.ackDelayByTpMu.Unlock()
	require.False(t, kept)
}
