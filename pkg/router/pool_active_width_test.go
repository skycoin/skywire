package router

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// TestActiveTunnelTargetsPoolActiveWidthByDefault pins the default that makes
// the standby pool's packet-level half happen: a role-reporting app dials one
// leg per tunnel, and the arbiter must still see a width to fill on the ACTIVE
// one, while a standby or role-less group keeps its dial-time width.
func TestActiveTunnelTargetsPoolActiveWidthByDefault(t *testing.T) {
	const app = "skysocks-client-active-width-test"
	r := newPoolTestRouter(t)
	exit, _ := cipher.GenerateKeyPair()
	local, _ := cipher.GenerateKeyPair()
	hops := []routing.Hop{{TpID: uuid.New(), From: local, To: exit}}

	active, _ := poolTunnel(t, r, exit, local, 49260, "", hops, 40, 0)
	active.SetAppName(app)
	active.SetTunnelRole(tunnelRoleActive)
	active.SetSelfHeal(nil, 1)
	require.Equal(t, 2, active.muxWidthTarget(),
		"an active tunnel dialed with one leg must target pool.active_width (2) so the arbiter can take a pool leg under load")

	active.SetSelfHeal(nil, 3)
	require.Equal(t, 3, active.muxWidthTarget(), "a larger dial-time width is kept")

	standby, _ := poolTunnel(t, r, exit, local, 49261, "", hops, 40, 0)
	standby.SetAppName(app)
	standby.SetTunnelRole(tunnelRoleStandby)
	standby.SetSelfHeal(nil, 2)
	require.Equal(t, 1, standby.muxWidthTarget(), "a standby tunnel stays single-leg")

	pending, _ := poolTunnel(t, r, exit, local, 49262, "", hops, 40, 0)
	pending.SetAppName(app)
	pending.SetSelfHeal(nil, 1)
	require.Equal(t, 1, pending.muxWidthTarget(), "a group whose role has not landed keeps its dial-time width")
}
