// Package router pkg/router/pool_refill_owner_test.go c2-net-routing
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
)

// An active tunnel short a leg dials its own replacement only while the
// standby pool cannot give one up. Once it can, the arbiter owns the refill.
func TestPoolOwnsRefillOfAnActiveTunnel(t *testing.T) {
	r := newPoolTestRouter(t)
	exit, _ := cipher.GenerateKeyPair()
	local, _ := cipher.GenerateKeyPair()
	rg, _ := poolTunnel(t, r, exit, local, 49195, tunnelRoleActive,
		[]routing.Hop{{TpID: uuid.New(), From: local, To: exit}}, 40, 0)
	rg.SetAppName("skynet-client-refill-test")
	rg.SetTunnelRole(tunnelRoleActive)

	dialed := make(chan struct{}, 8)
	rg.SetSelfHeal(func([]string) { dialed <- struct{}{} }, 2)
	minStandby := rg.mux.knInt(routersettings.PoolMinStandby)

	rg.poolStandbys.Store(int32(minStandby)) //nolint:gosec // test value
	require.False(t, rg.poolOwnsRefill(), "a pool at its reserve cannot give a tunnel up")
	rg.maybeSelfHeal()
	select {
	case <-dialed:
	case <-time.After(5 * time.Second):
		t.Fatal("with no spare in the pool the tunnel must dial its own replacement")
	}
	require.Eventually(t, func() bool { return !rg.healInFlight.Load() }, 5*time.Second, time.Millisecond)
	for len(dialed) > 0 {
		<-dialed
	}

	rg.poolStandbys.Store(int32(minStandby + 1)) //nolint:gosec // test value
	require.True(t, rg.poolOwnsRefill(), "a pool above its reserve owns the refill")
	rg.maybeSelfHeal()
	rg.topUpBelowTarget()
	select {
	case <-dialed:
		t.Fatal("the tunnel dialed a replacement the pool should have supplied")
	case <-time.After(200 * time.Millisecond):
	}

	rg.SetTunnelRole(tunnelRoleStandby)
	require.False(t, rg.poolOwnsRefill(), "only an active tunnel takes from the pool")
}
