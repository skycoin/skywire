package router

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The app names a tunnel by the local port it dialed from; the router fills in
// the route behind it. This is the whole seam, and the event it writes is what
// `visor state --select diag` shows after a failover.
func TestNoteTunnelEvent_RecordsOnTheGroupAndRelabelsIt(t *testing.T) {
	r := newLegTestRouter(t)
	rg, desc := r.setupInitializingPrimary(t)
	rg.muxEvents = &r.muxEvents
	rg.SetTunnelRole(TunnelRoleStandbyLabel)

	// The group has to be an ESTABLISHED one: a tunnel the app is switching is
	// past its handshake by definition.
	r.mx.Lock()
	r.rgsNs[desc] = &NoiseRouteGroup{rg: rg}
	r.mx.Unlock()

	require.True(t, r.NoteTunnelEvent(desc.SrcPort(), MuxEventTunnelPromoted,
		"failover: active tunnel died", TunnelRoleActiveLabel))

	ev := r.MuxEvents()
	require.Len(t, ev, 1, "the ring is attached after the primary leg, so the promote is all of it")
	last := ev[0]
	require.Equal(t, MuxEventTunnelPromoted, last.Event)
	require.Equal(t, "failover: active tunnel died", last.Reason)
	require.Equal(t, MuxByAdaptive, last.By)
	require.Equal(t, desc.SrcPort(), last.Desc.SrcPort, "the bench filters on the group's own ports")
	require.Equal(t, desc.DstPort(), last.Desc.DstPort)
	require.NotZero(t, last.TpID, "a promote must say which first hop took over")

	// ...and the role the group reports NOW is the one the app just set, not
	// the one it was dialed with.
	require.Equal(t, TunnelRoleActiveLabel, rg.TunnelRole())
	require.Equal(t, TunnelRoleActiveLabel, rg.MuxStats().TunnelRole)
}

// An empty role leaves the label alone: a retire says a tunnel is gone, not
// what it became.
func TestNoteTunnelEvent_EmptyRoleLeavesTheLabel(t *testing.T) {
	r := newLegTestRouter(t)
	rg, desc := r.setupInitializingPrimary(t)
	rg.muxEvents = &r.muxEvents
	rg.SetTunnelRole(TunnelRoleActiveLabel)
	r.mx.Lock()
	r.rgsNs[desc] = &NoiseRouteGroup{rg: rg}
	r.mx.Unlock()

	require.True(t, r.NoteTunnelEvent(desc.SrcPort(), MuxEventTunnelRetired, "liveness: no pong for 47s", ""))
	require.Equal(t, TunnelRoleActiveLabel, rg.TunnelRole())
	require.Equal(t, MuxEventTunnelRetired, r.MuxEvents()[0].Event)
}

// A port with no route group behind it is not an error — the group a retire
// reports on may already be reaped — and an unnamed event is refused outright.
func TestNoteTunnelEvent_UnknownPortAndEmptyEvent(t *testing.T) {
	r := newLegTestRouter(t)
	rg, desc := r.setupInitializingPrimary(t)
	rg.muxEvents = &r.muxEvents
	r.mx.Lock()
	r.rgsNs[desc] = &NoiseRouteGroup{rg: rg}
	r.mx.Unlock()

	require.False(t, r.NoteTunnelEvent(legTestSrcPort+1, MuxEventTunnelParked, "gone", ""))
	require.False(t, r.NoteTunnelEvent(desc.SrcPort(), "", "no event kind", ""))
	require.False(t, r.NoteTunnelEvent(0, MuxEventTunnelParked, "no port", ""))
	require.Empty(t, r.MuxEvents(), "nothing was recorded")
}

// The role strings the skysocks client sends. Duplicated as local constants
// rather than imported: pkg/skysocks imports pkg/router, so the dependency
// cannot run the other way.
const (
	TunnelRoleActiveLabel  = "active"
	TunnelRoleStandbyLabel = "standby"
)
