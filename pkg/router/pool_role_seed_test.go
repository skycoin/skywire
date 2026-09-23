// Package router pkg/router/pool_role_seed_test.go
//
// The role has to be on the group BEFORE the dial decides how wide it may be.
// The rig run of 2026-09-22 (bench/2026-09-22/63bb131cf-smoke, compose set at
// visor mux width 2) ended with all 30 standby groups holding 2 legs, 33
// leg_added and ZERO pool_leg_taken: the widening was dial-time, not the
// arbiter's, because the group's own role field was written by finishDial —
// after the mux width was read, after SetSelfHeal was wired, and after
// establishMuxRoutes had been launched.
package router

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// TestStandbySeededAtCreationNeverGetsASecondLeg is the gate: a tunnel dialed
// with TunnelRole standby, under a visor mux width of 2, is single-leg through
// every path that could widen it — the dial's own width (which is what gates
// establishMuxRoutes and the SetSelfHeal wiring), the self-heal top-up, the
// adaptive re-cap, and the arbiter.
func TestStandbySeededAtCreationNeverGetsASecondLeg(t *testing.T) {
	const width = 2
	const app = "skysocks-client-roleseed-test"
	r := newPoolTestRouter(t)
	exit, _ := cipher.GenerateKeyPair()
	local, _ := cipher.GenerateKeyPair()

	// What saveRouteGroupRules now does at creation: app name, then the role
	// from DialOptions.TunnelRole — before anything reads the mux width.
	standby, _ := poolTunnel(t, r, exit, local, 49250, "",
		[]routing.Hop{{TpID: uuid.New(), From: local, To: exit}}, 40, 0)
	standby.SetAppName(app)
	standby.SetTunnelRole(tunnelRoleStandby)

	// The dial's width gate: this is what keeps SetSelfHeal and the background
	// establishMuxRoutes — both behind `muxTarget > 1` — from ever running.
	require.Equal(t, 1, standby.dialMuxTarget(width),
		"a standby tunnel's dial must be clamped to one leg, so establishMuxRoutes never runs for it")
	require.False(t, standby.poolWideningAllowed())

	// The self-heal top-up and the adaptive re-cap, for the same group.
	widened := 0
	standby.SetSelfHeal(func([]string) { widened++ }, width)
	require.Equal(t, 1, standby.muxWidthTarget(), "the width offered to a standby tunnel is clamped to one leg")
	standby.setSelfHealTarget(width)
	require.Equal(t, 1, standby.muxWidthTarget(), "an adaptive re-tune must not widen a standby tunnel either")
	standby.maybeSelfHeal()
	require.Zero(t, widened, "nothing may widen a standby tunnel")

	// A group of the same app whose role has not landed yet is treated the
	// same way — the app is known to report roles, so "no role" is in flight.
	pending, _ := poolTunnel(t, r, exit, local, 49251, "",
		[]routing.Hop{{TpID: uuid.New(), From: local, To: exit}}, 40, 0)
	pending.SetAppName(app)
	require.True(t, appReportsTunnelRoles(app), "a dial that carried a role registers its app at creation")
	require.Equal(t, 1, pending.dialMuxTarget(width), "a role that has not landed yet is not a license to widen")

	// An ACTIVE tunnel of the same app keeps the visor width, at dial time and
	// afterwards: the clamp is about the role, not about the app.
	active, _ := poolTunnel(t, r, exit, local, 49252, "",
		[]routing.Hop{{TpID: uuid.New(), From: local, To: exit}}, 40, 0)
	active.SetAppName(app)
	active.SetTunnelRole(tunnelRoleActive)
	require.Equal(t, width, active.dialMuxTarget(width), "an active tunnel is dialed at the visor mux width")
	active.SetSelfHeal(func([]string) {}, width)
	require.Equal(t, width, active.muxWidthTarget(), "an active tunnel keeps its self-heal width")

	// And a dial with no role at all, from an app that never labels anything,
	// is byte-for-byte what it was.
	plain, _ := poolTunnel(t, r, exit, local, 49253, "",
		[]routing.Hop{{TpID: uuid.New(), From: local, To: exit}}, 40, 0)
	plain.SetAppName("an-app-that-never-labels-anything")
	require.Equal(t, width, plain.dialMuxTarget(width), "an app that never reports roles is unaffected")
}
