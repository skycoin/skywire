package router

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// TestTopUpBelowTargetRetriesOneLeg pins the periodic retry behind self-heal:
// an active group short of its target dials exactly one replacement per call,
// never while a heal is already dialing, and not at all once it is at target.
func TestTopUpBelowTargetRetriesOneLeg(t *testing.T) {
	r := newPoolTestRouter(t)
	exit, _ := cipher.GenerateKeyPair()
	local, _ := cipher.GenerateKeyPair()
	hops := []routing.Hop{{TpID: uuid.New(), From: local, To: exit}}

	g, _ := poolTunnel(t, r, exit, local, 49270, "", hops, 40, 0)
	g.SetTunnelRole(tunnelRoleActive)
	var adds atomic.Int32
	g.SetSelfHeal(func([]string) { adds.Add(1) }, 2)

	g.topUpBelowTarget()
	require.Eventually(t, func() bool { return adds.Load() == 1 && !g.healInFlight.Load() },
		time.Second, 10*time.Millisecond, "a group one leg short of target dials one replacement")

	g.healInFlight.Store(true)
	g.topUpBelowTarget()
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(1), adds.Load(), "no retry while a heal is already dialing")
	g.healInFlight.Store(false)

	g.SetSelfHeal(func([]string) { adds.Add(1) }, 1)
	g.topUpBelowTarget()
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(1), adds.Load(), "a group at its target dials nothing")
}
