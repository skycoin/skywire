package router

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// countingDiscovery counts whole-graph fetches: on a visor each one is a
// subscription to TPD's routing feed.
type countingDiscovery struct {
	transport.DiscoveryClient
	all     atomic.Int32
	entries []*transport.Entry
}

func (d *countingDiscovery) GetAllTransports(context.Context) ([]*transport.Entry, error) {
	d.all.Add(1)
	return d.entries, nil
}

func newHopLookupRouter(t *testing.T, dc transport.DiscoveryClient) *router {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	tm, err := transport.NewManager(nil, nil, nil, &transport.ManagerConfig{
		PubKey: pk, SecKey: sk, DiscoveryClient: dc,
	}, network.ClientFactory{})
	require.NoError(t, err)
	return &router{logger: logging.MustGetLogger("test_hop_lookups"), tm: tm, tpdCache: newTPDSnapshotCache()}
}

// A route-finder route describes its hops, so ranking it fetches nothing.
func TestBuildHopLookups_DescribedHopsNeedNoSnapshot(t *testing.T) {
	dc := &countingDiscovery{DiscoveryClient: transport.NewDiscoveryMock()}
	r := newHopLookupRouter(t, dc)

	id1, id2 := uuid.New(), uuid.New()
	path := []routing.Hop{
		{TpID: id1, Latency: 12, Type: string(tptypes.STCPR), ThroughputBps: 4e6},
		{TpID: id2, Type: string(tptypes.SUDPH)}, // unmeasured edge
	}
	latencyFor, typeFor, throughputFor := r.buildHopLookups(context.Background(), [][]routing.Hop{path}, nil)

	require.Zero(t, dc.all.Load(), "a described route must not fetch the transport graph")
	require.Equal(t, 12.0, latencyFor(id1))
	require.Equal(t, string(tptypes.STCPR), typeFor(id1))
	require.Equal(t, 4e6, throughputFor(id1))
	require.Zero(t, latencyFor(id2))
	require.Equal(t, string(tptypes.SUDPH), typeFor(id2))
}

// An older route-finder sends bare hops; those still come from the snapshot.
func TestBuildHopLookups_BareHopsUseSnapshot(t *testing.T) {
	id := uuid.New()
	dc := &countingDiscovery{
		DiscoveryClient: transport.NewDiscoveryMock(),
		entries:         []*transport.Entry{{ID: id, Type: tptypes.STCPR, Latency: 30, ThroughputBps: 1e6}},
	}
	r := newHopLookupRouter(t, dc)

	latencyFor, typeFor, throughputFor := r.buildHopLookups(context.Background(), [][]routing.Hop{{{TpID: id}}}, nil)

	require.Equal(t, int32(1), dc.all.Load())
	require.Equal(t, 30.0, latencyFor(id))
	require.Equal(t, string(tptypes.STCPR), typeFor(id))
	require.Equal(t, 1e6, throughputFor(id))
}

// With dial.local_graph off the visor never fetches the whole graph: bare
// route-finder hops go unranked and local calculation answers only with a
// direct transport.
func TestLocalGraphOffFetchesNothing(t *testing.T) {
	require.NoError(t, routersettings.SetValue(routersettings.DialLocalGraph.Name(), routersettings.BoolBits(false)))
	t.Cleanup(func() {
		_ = routersettings.SetValue(routersettings.DialLocalGraph.Name(), routersettings.BoolBits(true)) //nolint:errcheck
	})

	id := uuid.New()
	dc := &countingDiscovery{
		DiscoveryClient: transport.NewDiscoveryMock(),
		entries:         []*transport.Entry{{ID: id, Type: tptypes.STCPR, Latency: 30}},
	}
	r := newHopLookupRouter(t, dc)

	latencyFor, _, _ := r.buildHopLookups(context.Background(), [][]routing.Hop{{{TpID: id}}}, nil)
	require.Zero(t, latencyFor(id))

	_, _, _, err := r.localTransportGraph(context.Background(), dc)
	require.ErrorIs(t, err, errLocalGraphOff)
	require.Zero(t, dc.all.Load(), "the transport graph must not be fetched")

	// Back on, the graph is fetched once and cached.
	require.NoError(t, routersettings.SetValue(routersettings.DialLocalGraph.Name(), routersettings.BoolBits(true)))
	g, all, _, err := r.localTransportGraph(context.Background(), dc)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, 30.0, g.latencyByID[id])
	require.Equal(t, int32(1), dc.all.Load())
}
