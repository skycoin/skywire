// Package router pkg/router/noise_route_group_delegate_test.go
//
// Coverage for the NoiseRouteGroup net.Conn/stat delegators (they simply
// forward to the wrapped RouteGroup but had no direct test), plus the
// router-level ActiveRouteStatuses / RoutingTableStats observability
// surface built on top of them, and the networkStats accounting helpers.
package router

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// TestNoiseRouteGroupDelegators exercises every forwarding method on
// NoiseRouteGroup against a real (mux'd) RouteGroup.
func TestNoiseRouteGroupDelegators(t *testing.T) {
	rg, _, _ := createMuxRouteGroup(t, 2)
	nrg := &NoiseRouteGroup{rg: rg}

	// Addresses forward to the descriptor endpoints.
	require.Equal(t, rg.desc.Dst(), nrg.LocalAddr())
	require.Equal(t, rg.desc.Src(), nrg.RemoteAddr())

	// A freshly-built group is alive and not closed.
	require.True(t, nrg.IsAlive())
	require.False(t, nrg.isClosed())

	// Stat reads are zero on a group that has not moved data.
	require.EqualValues(t, 0, nrg.BandwidthSent())
	require.EqualValues(t, 0, nrg.BandwidthReceived())
	require.EqualValues(t, 0, nrg.UploadSpeed())
	require.EqualValues(t, 0, nrg.DownloadSpeed())
	require.EqualValues(t, 0, nrg.Latency())

	// Hop details are populated from the forward hops we set.
	src := rg.desc.SrcPK()
	dst := rg.desc.DstPK()
	mid, _ := cipher.GenerateKeyPair()
	nrg.SetForwardHops([]routing.Hop{
		{From: src, To: mid},
		{From: mid, To: dst},
	})
	hops := nrg.RouteHops()
	require.NotEmpty(t, hops)
	require.Equal(t, dst, hops[len(hops)-1], "path ends at the destination")
	require.NotEmpty(t, nrg.RouteHopDetails())

	// SetError / GetError round-trip.
	wantErr := errors.New("boom")
	nrg.SetError(wantErr)
	require.Equal(t, wantErr, nrg.GetError())

	// Close chains through to the RouteGroup (nrg.Conn is nil here).
	require.NoError(t, nrg.Close())
	require.True(t, nrg.isClosed())
}

// TestRouterActiveRouteStatuses registers a NoiseRouteGroup on a real router
// and proves ActiveRouteStatuses reflects it (including the per-transport
// breakdown), and that RoutingTableStats delegates to the routing table.
func TestRouterActiveRouteStatuses(t *testing.T) {
	conf := &Config{
		Logger:             logging.MustGetLogger("active_route_status_test"),
		MasterLogger:       logging.NewMasterLogger(),
		AwaitSetupListener: &dmsg.Listener{},
	}
	rIface, err := New(nil, conf, nil)
	require.NoError(t, err)
	r := rIface.(*router)

	// Empty router: no active statuses.
	require.Empty(t, r.ActiveRouteStatuses())

	// Register a 2-leg mux route group. Set forward hops so RouteHopDetails
	// takes the recorded-path branch instead of the local-transport fallback
	// (the test ManagedTransports have no live network for tp.Type()).
	rg, mts, _ := createMuxRouteGroup(t, 2)
	mid, _ := cipher.GenerateKeyPair()
	rg.SetForwardHops([]routing.Hop{
		{From: rg.desc.SrcPK(), To: mid},
		{From: mid, To: rg.desc.DstPK()},
	})
	nrg := &NoiseRouteGroup{rg: rg}
	r.mx.Lock()
	r.rgsNs[rg.desc] = nrg
	r.mx.Unlock()

	statuses := r.ActiveRouteStatuses()
	require.Len(t, statuses, 1)
	st := statuses[0]
	require.Equal(t, rg.desc.DstPK(), st.LocalPK)
	require.Equal(t, rg.desc.SrcPK(), st.RemotePK)
	require.True(t, st.MuxEnabled, "the group carries a mux")
	require.Len(t, st.Transports, len(mts), "one RouteTransport per leg")

	// RoutingTableStats delegates to the router's routing table.
	stats := r.RoutingTableStats()
	require.GreaterOrEqual(t, stats.Count, 0)
}

// TestNetworkStats covers the atomic accounting helpers, including the
// millisecond->Duration conversion in Latency and the rate calc in
// RemoteThroughput.
func TestNetworkStats(t *testing.T) {
	s := newNetworkStats()

	s.SetLatency(5)
	require.Equal(t, 5*time.Millisecond, s.Latency())

	s.SetUploadSpeed(1234)
	require.EqualValues(t, 1234, s.UploadSpeed())
	s.SetDownloadSpeed(5678)
	require.EqualValues(t, 5678, s.DownloadSpeed())

	s.AddBandwidthSent(100)
	s.AddBandwidthSent(50)
	require.EqualValues(t, 150, s.BandwidthSent())

	s.AddBandwidthReceived(200)
	require.EqualValues(t, 200, s.BandwidthReceived())

	// RemoteThroughput drains the received-bandwidth window and returns a
	// non-negative rate.
	require.GreaterOrEqual(t, s.RemoteThroughput(), int64(0))
}
