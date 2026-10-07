// Package router pkg/router/direct_setup_test.go c2-net-routing
package router

import (
	"context"
	"net"
	"testing"
	"time"

	rpc "github.com/0magnet/gobrpc"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router/emu"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// directTestRouter is a router keyed pk holding one live transport to peer.
func directTestRouter(t *testing.T, pk, peer cipher.PubKey, tpID uuid.UUID) *router {
	t.Helper()
	_, sk := cipher.GenerateKeyPair()
	tm, err := transport.NewManager(nil, nil, nil, &transport.ManagerConfig{
		PubKey: pk, SecKey: sk, DiscoveryClient: transport.NewDiscoveryMock(),
	}, network.ClientFactory{})
	require.NoError(t, err)
	a, b := emu.NewPair(emu.PairConfig{Name: "direct", APK: pk, BPK: peer, Type: tptypes.STCPR})
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() }) //nolint:errcheck
	mt := transport.NewManagedTransportForTest(a)
	mt.Entry.ID = tpID
	mt.Entry.Type = tptypes.STCPR
	mt.Entry.Edges = transport.SortEdges(pk, peer)
	tm.InjectTransportForTest(mt)
	return &router{
		logger:        logging.MustGetLogger("direct_setup_test"),
		mLogger:       logging.NewMasterLogger(),
		conf:          &Config{PubKey: pk, SecKey: sk},
		tm:            tm,
		rt:            routing.NewTable(logging.MustGetLogger("direct_setup_rt")),
		rgsNs:         make(map[routing.RouteDescriptor]*NoiseRouteGroup),
		rgsRaw:        make(map[routing.RouteDescriptor]*RouteGroup),
		rgsDatagrams:  make(map[routing.RouteDescriptor]*DatagramRouteGroup),
		datagramPorts: make(map[routing.Port]struct{}),
		pending:       newPendingPackets(),
		pendingLegs:   newPendingLegs(),
		accept:        make(chan routing.EdgeRules, acceptSize),
		done:          make(chan struct{}),
	}
}

// serveDirectGateway serves dst's direct setup gateway to caller over a pipe
// and returns the caller's client end.
func serveDirectGateway(t *testing.T, dst *router, caller cipher.PubKey) *Client {
	t.Helper()
	cliConn, srvConn := net.Pipe()
	srv := rpc.NewServer()
	require.NoError(t, registerDirectSetupRPC(srv, &directSetupGateway{r: dst, caller: caller, log: dst.logger}))
	go srv.ServeConn(srvConn)
	c := NewClientFromRaw(cliConn, dst.conf.PubKey)
	t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
	return c
}

func oneHopRequest(src, dst cipher.PubKey, tpID uuid.UUID) routing.BidirectionalRoute {
	return routing.BidirectionalRoute{
		Desc:      routing.NewRouteDescriptor(src, dst, 100, 200),
		KeepAlive: time.Minute,
		Forward:   []routing.Hop{{TpID: tpID, From: src, To: dst}},
		Reverse:   []routing.Hop{{TpID: tpID, From: dst, To: src}},
	}
}

// Two visors with a transport between them set up a one-hop route with no
// setup node: the source gets its edge, and the destination installs its own
// and hands the new group to AcceptRoutes.
func TestDirectSetup_OneHopWithoutSetupNode(t *testing.T) {
	aPK, _ := cipher.GenerateKeyPair()
	bPK, _ := cipher.GenerateKeyPair()
	tpID := uuid.New()
	a := directTestRouter(t, aPK, bPK, tpID)
	b := directTestRouter(t, bPK, aPK, tpID)
	require.True(t, b.directSetupPeer(aPK))

	req := oneHopRequest(aPK, bPK, tpID)
	rules, err := a.setupDirectWith(context.Background(), serveDirectGateway(t, b, aPK), req)
	require.NoError(t, err)
	require.NoError(t, rules.Validate())
	require.Equal(t, routing.RuleForward, rules.Forward.Type())
	require.Equal(t, tpID, rules.Forward.NextTransportID())

	select {
	case got := <-b.accept:
		require.Equal(t, bPK, got.Desc.DstPK())
		require.Equal(t, aPK, got.Desc.SrcPK())
		require.Equal(t, tpID, got.Forward.NextTransportID())
		// Each side's forward rule names the route ID the other side consumes.
		require.Equal(t, got.Reverse.KeyRouteID(), rules.Forward.NextRouteID())
		require.Equal(t, rules.Reverse.KeyRouteID(), got.Forward.NextRouteID())
	case <-time.After(5 * time.Second):
		t.Fatal("the destination did not take the route")
	}
}

// The destination installs nothing it was not asked to by one of the route's
// own edges, over a transport they share.
func TestDirectSetup_RefusesWhatIsNotADirectRoute(t *testing.T) {
	aPK, _ := cipher.GenerateKeyPair()
	bPK, _ := cipher.GenerateKeyPair()
	cPK, _ := cipher.GenerateKeyPair()
	tpID := uuid.New()
	a := directTestRouter(t, aPK, bPK, tpID)
	b := directTestRouter(t, bPK, aPK, tpID)
	ctx := context.Background()

	// A third key may not set up a route on a's behalf.
	_, err := a.setupDirectWith(ctx, serveDirectGateway(t, b, cPK), oneHopRequest(aPK, bPK, tpID))
	require.Error(t, err)

	// Nor over a transport b does not hold.
	_, err = a.setupDirectWith(ctx, serveDirectGateway(t, b, aPK), oneHopRequest(aPK, bPK, uuid.New()))
	require.Error(t, err)

	// One reservation per connection, and a bounded one.
	c := serveDirectGateway(t, b, aPK)
	_, err = c.ReserveIDs(ctx, maxDirectSetupIDs+1)
	require.Error(t, err)
	_, err = c.ReserveIDs(ctx, 2)
	require.NoError(t, err)
	_, err = c.ReserveIDs(ctx, 2)
	require.Error(t, err)

	// Never a transit hop.
	_, err = c.AddIntermediaryRules(ctx, nil)
	require.Error(t, err)

	require.Empty(t, b.accept)
}

// A peer that predates direct setup closes the setup connection unanswered,
// and only that marks it for the setup node.
func TestRefusedDirectSetup(t *testing.T) {
	cliConn, srvConn := net.Pipe()
	require.NoError(t, srvConn.Close())
	c := NewClientFromRaw(cliConn, cipher.PubKey{})
	_, err := c.ReserveIDs(context.Background(), 2)
	require.True(t, refusedDirectSetup(err), "got %v", err)

	require.False(t, refusedDirectSetup(&DialError{Err: context.DeadlineExceeded}))
}
