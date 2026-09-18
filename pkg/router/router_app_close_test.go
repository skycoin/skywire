package router

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// appTaggedRGRemotePort is the single remote port every group in this file
// faces; the groups are distinguished by their local port alone.
const appTaggedRGRemotePort routing.Port = 3

// newAppTaggedRG builds an established route group tagged with appName and
// registers nothing — the caller decides which map it goes in. Every group in
// this test faces the same remote port; only lPort distinguishes them.
func newAppTaggedRG(t *testing.T, rt routing.Table, appName string, lPort routing.Port) *RouteGroup {
	t.Helper()
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	desc := routing.NewRouteDescriptor(pk1, pk2, lPort, appTaggedRGRemotePort)
	rg := NewRouteGroup(DefaultRouteGroupConfig(), rt, desc, logging.NewMasterLogger())
	rg.SetAppName(appName)
	return rg
}

// TestCloseRouteGroupsForApp_ClosesEveryGroupTheAppDialed is the regression
// test for route groups outliving their app: two tunnels dialed for one app
// (what `--tunnels 2` produces — sibling groups, same app tag, different
// descriptors) must BOTH close and leave the registry when the app stops.
//
// Before this, the only reapers were the app conn's own Close (which never
// fires for a group the app never took delivery of) and the rules GC (which
// never collects rules a live keep-alive loop keeps refreshing), so the group
// lived on: listed by `proxy mux info` for a stopped app, and counted by
// `proxy start --route`'s reconcile check.
func TestCloseRouteGroupsForApp_ClosesEveryGroupTheAppDialed(t *testing.T) {
	r := newSweepTestRouter(t)

	tunnel1 := newAppTaggedRG(t, r.rt, "skysocks-client", 49168)
	tunnel2 := newAppTaggedRG(t, r.rt, "skysocks-client", 49201)
	r.rgsNs[tunnel1.desc] = &NoiseRouteGroup{rg: tunnel1, Conn: tunnel1}
	r.rgsNs[tunnel2.desc] = &NoiseRouteGroup{rg: tunnel2, Conn: tunnel2}

	// Another app's group must survive untouched.
	other := newAppTaggedRG(t, r.rt, "vpn-client", 49300)
	r.rgsNs[other.desc] = &NoiseRouteGroup{rg: other, Conn: other}

	// A group still finishing its noise handshake belongs to the app too.
	initializing := newAppTaggedRG(t, r.rt, "skysocks-client", 49222)
	r.rgsRaw[initializing.desc] = initializing

	require.Len(t, r.RouteGroupMuxInfoForApp("skysocks-client"), 2)

	closed := r.CloseRouteGroupsForApp("skysocks-client")
	require.Equal(t, 3, closed)

	require.Empty(t, r.RouteGroupMuxInfoForApp("skysocks-client"),
		"no route group may be reported for a stopped app")
	require.Len(t, r.rgsNs, 1, "only the other app's group may remain registered")
	_, otherStillThere := r.rgsNs[other.desc]
	require.True(t, otherStillThere, "another app's route group must not be closed")
	require.Empty(t, r.rgsRaw, "the app's initializing group must be de-registered too")

	require.True(t, tunnel1.isClosed(), "tunnel 1 must be closed")
	require.True(t, tunnel2.isClosed(), "tunnel 2 must be closed")
	require.True(t, initializing.isClosed(), "the initializing group must be closed")
	require.False(t, other.isClosed(), "another app's group must stay open")
}

// TestCloseRouteGroupsForApp_UntaggedAndUnknownApps checks the guards: an
// empty app name closes nothing (it would otherwise match every untagged
// group), and an app with no groups is a no-op.
func TestCloseRouteGroupsForApp_UntaggedAndUnknownApps(t *testing.T) {
	r := newSweepTestRouter(t)

	untagged := newAppTaggedRG(t, r.rt, "", 49400)
	r.rgsNs[untagged.desc] = &NoiseRouteGroup{rg: untagged, Conn: untagged}

	require.Equal(t, 0, r.CloseRouteGroupsForApp(""))
	require.Equal(t, 0, r.CloseRouteGroupsForApp("skysocks-client"))
	require.Len(t, r.rgsNs, 1)
	require.False(t, untagged.isClosed())
}
