// Package router pkg/router/setup_batch_test.go
package router

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/routing"
)

// countingGateway is mockRouterGateway with per-method call counts and a switch
// to refuse intermediary rules — enough to assert both the coalescing (how many
// times each hop was asked) and partial failure.
type countingGateway struct {
	mu sync.Mutex

	lastRtID uint32

	reserveCalls int
	reservedIDs  int
	interCalls   int
	interRules   int
	edgeCalls    int
	deleted      []routing.RouteID

	refuseInter bool
}

func (gw *countingGateway) ReserveIDs(n uint8, routeIDs *[]routing.RouteID) error {
	gw.mu.Lock()
	defer gw.mu.Unlock()
	gw.reserveCalls++
	gw.reservedIDs += int(n)
	out := make([]routing.RouteID, n)
	for i := range out {
		gw.lastRtID++
		out[i] = routing.RouteID(gw.lastRtID)
	}
	*routeIDs = out
	return nil
}

func (gw *countingGateway) AddIntermediaryRules(rules []routing.Rule, ok *bool) error {
	gw.mu.Lock()
	defer gw.mu.Unlock()
	gw.interCalls++
	gw.interRules += len(rules)
	*ok = !gw.refuseInter
	return nil
}

func (gw *countingGateway) AddEdgeRules(_ routing.EdgeRules, ok *bool) error {
	gw.mu.Lock()
	defer gw.mu.Unlock()
	gw.edgeCalls++
	*ok = true
	return nil
}

func (gw *countingGateway) DelRules(ids []routing.RouteID, deleted *[]routing.RouteID) error {
	gw.mu.Lock()
	defer gw.mu.Unlock()
	gw.deleted = append(gw.deleted, ids...)
	*deleted = ids
	return nil
}

func snapshotOf(gw *countingGateway) countingGateway {
	gw.mu.Lock()
	defer gw.mu.Unlock()
	return countingGateway{
		reserveCalls: gw.reserveCalls,
		reservedIDs:  gw.reservedIDs,
		interCalls:   gw.interCalls,
		interRules:   gw.interRules,
		edgeCalls:    gw.edgeCalls,
		deleted:      append([]routing.RouteID(nil), gw.deleted...),
	}
}

// batchFixture builds n two-hop routes S->I_k->D over n distinct intermediates,
// with a counting gateway per visor.
func batchFixture(t *testing.T, n int) (
	batch routing.BidirectionalRouteBatch,
	gws map[cipher.PubKey]*countingGateway,
	src, dst cipher.PubKey,
	inters []cipher.PubKey,
) {
	t.Helper()
	src, _ = cipher.GenerateKeyPair()
	dst, _ = cipher.GenerateKeyPair()
	gws = map[cipher.PubKey]*countingGateway{src: {}, dst: {}}
	for i := 0; i < n; i++ {
		inter, _ := cipher.GenerateKeyPair()
		inters = append(inters, inter)
		gws[inter] = &countingGateway{}
		fwd := []routing.Hop{
			{TpID: uuid.New(), From: src, To: inter},
			{TpID: uuid.New(), From: inter, To: dst},
		}
		rev := []routing.Hop{
			{TpID: uuid.New(), From: dst, To: inter},
			{TpID: uuid.New(), From: inter, To: src},
		}
		batch.Routes = append(batch.Routes, routing.BatchRouteRequest{
			ID: uint32(i), //nolint:gosec
			Route: routing.BidirectionalRoute{
				Desc:    routing.NewRouteDescriptor(src, dst, 1, 2),
				Forward: fwd,
				Reverse: rev,
			},
		})
	}
	return batch, gws, src, dst, inters
}

// The whole point of the batch: the peers COMMON to every route — the source
// and the destination — are asked for route IDs once, not once per route, and
// they are asked for the full count the batch needs in that one call.
func TestCreateRouteGroupBatch_CoalescesSharedHops(t *testing.T) {
	const n = 8
	batch, gws, src, dst, inters := batchFixture(t, n)
	require.NoError(t, batch.Check())

	gateways := make(map[cipher.PubKey]interface{}, len(gws))
	for pk, gw := range gws {
		gateways[pk] = gw
	}
	dialer := newMockDialer(t, gateways)

	results, savings := CreateRouteGroupBatch(context.Background(), dialer, nil, batch, setupmetrics.NewEmpty())

	require.Len(t, results, n)
	for _, res := range results {
		require.False(t, res.Failed(), "route %d: %s", res.ID, res.Error)
	}

	// Source and destination: ONE reservation call each, covering every route.
	// Unbatched this is n calls each.
	require.Equal(t, 1, snapshotOf(gws[src]).reserveCalls, "source asked once for the whole batch")
	require.Equal(t, 1, snapshotOf(gws[dst]).reserveCalls, "destination asked once for the whole batch")
	// Each route needs one ID from the source on the forward path and one on
	// the reverse, so the single call covers 2n.
	require.Equal(t, 2*n, snapshotOf(gws[src]).reservedIDs)

	// Each intermediate appears in exactly one route, so one call each — the
	// coalescing cannot merge what is not shared, and must not double up.
	for _, i := range inters {
		s := snapshotOf(gws[i])
		require.Equal(t, 1, s.reserveCalls, "intermediate asked once")
		require.Equal(t, 1, s.interCalls, "one intermediary-rules install per intermediate")
	}

	// The destination edge is per route — not merged, and that is counted
	// honestly in the savings.
	require.Equal(t, n, snapshotOf(gws[dst]).edgeCalls)

	require.Equal(t, n, savings.Routes)
	require.Greater(t, savings.Coalesced, savings.Issued, "a batch must cost less than the same routes alone")
	require.Equal(t, savings.Coalesced-savings.Issued, savings.Saved())
}

// A batch whose members SHARE an intermediate collapses that hop's rule install
// too: one AddIntermediaryRules carrying every member's rules.
func TestCreateRouteGroupBatch_CoalescesASharedIntermediate(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	inter, _ := cipher.GenerateKeyPair()
	gws := map[cipher.PubKey]*countingGateway{src: {}, dst: {}, inter: {}}

	var batch routing.BidirectionalRouteBatch
	for i := 0; i < 3; i++ {
		batch.Routes = append(batch.Routes, routing.BatchRouteRequest{
			ID: uint32(i), //nolint:gosec
			Route: routing.BidirectionalRoute{
				Desc: routing.NewRouteDescriptor(src, dst, routing.Port(10+i), 2), //nolint:gosec
				Forward: []routing.Hop{
					{TpID: uuid.New(), From: src, To: inter},
					{TpID: uuid.New(), From: inter, To: dst},
				},
				Reverse: []routing.Hop{
					{TpID: uuid.New(), From: dst, To: inter},
					{TpID: uuid.New(), From: inter, To: src},
				},
			},
		})
	}
	gateways := make(map[cipher.PubKey]interface{}, len(gws))
	for pk, gw := range gws {
		gateways[pk] = gw
	}

	results, _ := CreateRouteGroupBatch(context.Background(), newMockDialer(t, gateways), nil, batch, setupmetrics.NewEmpty())
	require.Len(t, results, 3)
	for _, res := range results {
		require.False(t, res.Failed(), "route %d: %s", res.ID, res.Error)
	}

	s := snapshotOf(gws[inter])
	require.Equal(t, 1, s.reserveCalls, "one reservation for all three routes")
	require.Equal(t, 1, s.interCalls, "ONE intermediary-rules install for all three routes")
	require.Equal(t, 6, s.interRules, "carrying every route's forward and reverse rule")
}

// Partial success: one intermediate refuses, its member fails and is torn down,
// the siblings install.
func TestCreateRouteGroupBatch_PartialFailure(t *testing.T) {
	const n = 4
	batch, gws, _, dst, inters := batchFixture(t, n)
	bad := inters[2]
	gws[bad].refuseInter = true

	gateways := make(map[cipher.PubKey]interface{}, len(gws))
	for pk, gw := range gws {
		gateways[pk] = gw
	}

	results, _ := CreateRouteGroupBatch(context.Background(), newMockDialer(t, gateways), nil, batch, setupmetrics.NewEmpty())
	require.Len(t, results, n)

	failed := 0
	for _, res := range results {
		if res.Failed() {
			failed++
			require.Contains(t, res.Error, bad.String())
			continue
		}
		require.NotZero(t, res.Rules.Desc)
	}
	require.Equal(t, 1, failed, "exactly the member whose intermediate refused")

	// The failed member's destination edge must not be left installed: only the
	// three that survived get one, and whatever the failed member did install is
	// deleted.
	require.Equal(t, n-1, snapshotOf(gws[dst]).edgeCalls,
		"the refused member never reaches the destination edge install")
	require.Empty(t, snapshotOf(gws[bad]).deleted,
		"nothing was installed on the refusing hop, so nothing to delete")
}

// A batch whose members are all invalid never reaches the network, and every
// member is still answered.
func TestCreateRouteGroupBatch_AllInvalidAnswersEveryMember(t *testing.T) {
	batch, gws, _, _, _ := batchFixture(t, 3)
	for i := range batch.Routes {
		batch.Routes[i].Route.Reverse = nil
	}
	gateways := make(map[cipher.PubKey]interface{}, len(gws))
	for pk, gw := range gws {
		gateways[pk] = gw
	}

	results, savings := CreateRouteGroupBatch(context.Background(), newMockDialer(t, gateways), nil, batch, setupmetrics.NewEmpty())
	require.Len(t, results, 3)
	for _, res := range results {
		require.True(t, res.Failed())
		require.Contains(t, res.Error, "invalid route")
	}
	require.Zero(t, savings.Issued)
}

// The kind counters and the batch histogram are what tell an operator whether a
// deployed client negotiated the batch form at all.
func TestCreateRouteGroupBatch_RecordsKindAndSavings(t *testing.T) {
	const n = 5
	batch, gws, _, _, _ := batchFixture(t, n)
	gateways := make(map[cipher.PubKey]interface{}, len(gws))
	for pk, gw := range gws {
		gateways[pk] = gw
	}
	c := setupmetrics.NewCollector(setupmetrics.CollectorConfig{})

	_, savings := CreateRouteGroupBatch(context.Background(), newMockDialer(t, gateways), nil, batch, c)

	snap := c.Snapshot()
	require.Equal(t, uint64(1), snap.RequestsByKind[setupmetrics.SetupKindBatch])
	require.Equal(t, uint64(n), snap.RoutesByKind[setupmetrics.SetupKindBatch])
	require.Equal(t, uint64(1), snap.Batch.Batches)
	require.Equal(t, uint64(n), snap.Batch.Routes)
	require.Equal(t, uint64(n), snap.Batch.Installed)
	require.Equal(t, uint64(1), snap.Batch.RoutesPerBatch[n])
	require.Equal(t, uint64(savings.Saved()), snap.Batch.PerHopRPCsSaved) //nolint:gosec
	require.Positive(t, snap.Batch.PerHopRPCsSaved)
}
