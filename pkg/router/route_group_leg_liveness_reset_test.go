package router

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/transport"
)

// TestRegrownLegStartsWithCleanLiveness: a leg pruned for missed echoes and
// re-grown over the same relay (transport IDs are deterministic per relay)
// must not inherit the old miss tally, or the first probe prunes it again.
func TestRegrownLegStartsWithCleanLiveness(t *testing.T) {
	rig := newComposeRig(t, "skysocks-client-liveness-reset-test", 0)
	g := rig.active

	id := uuid.New()
	g.legLivenessMu.Lock()
	g.legMissed[id] = 3
	g.legPongSeen[id] = false
	g.legLivenessMu.Unlock()

	mt := transport.NewManagedTransportForTest(newWorkingTransport())
	mt.Entry = transport.Entry{ID: id, Type: "test"}
	g.appendRules(g.fwd[0], g.rvs[0], mt, "test regrow")

	g.legLivenessMu.Lock()
	_, missed := g.legMissed[id]
	_, seen := g.legPongSeen[id]
	g.legLivenessMu.Unlock()
	require.False(t, missed, "a new leg must not inherit the pruned leg's miss count")
	require.False(t, seen)
}
