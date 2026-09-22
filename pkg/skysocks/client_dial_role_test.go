package skysocks

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDialTunnelRole_PoolFillStandbyActiveFillActive pins the role the app puts
// on each dial. It has to be right AT DIAL TIME: the visor refuses to widen a
// group of a role-reporting app while its role is unknown, so a pool fill that
// arrived role-less used to be widened before it was known to be spare (rig
// 2026-09-22 — 32 groups, 37 leg_added, standby tunnels holding two legs).
func TestDialTunnelRole_PoolFillStandbyActiveFillActive(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tunnels int
		routed  bool
		standby bool
		want    string
	}{
		{"the first tunnels of a --tunnels 2 session", 2, false, false, TunnelRoleActive},
		{"a re-dial refilling the active set", 2, false, false, TunnelRoleActive},
		{"an explicit --routed single tunnel", 1, true, false, TunnelRoleActive},
		{"a standby-pool fill", 2, false, true, TunnelRoleStandby},
		{"a standby-pool fill of a --routed session", 1, true, true, TunnelRoleStandby},
		{"a plain single-tunnel session has no pool and no role", 1, false, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, DialTunnelRole(tc.tunnels, tc.routed, tc.standby))
		})
	}
}
