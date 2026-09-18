// Package routing pkg/routing/batch_route_test.go
package routing

import (
	"bytes"
	"encoding/gob"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// twoHopRoute builds S -> I -> D with a deterministic reverse.
func twoHopRoute(src, inter, dst cipher.PubKey) BidirectionalRoute {
	fwd := []Hop{
		{TpID: uuid.New(), From: src, To: inter},
		{TpID: uuid.New(), From: inter, To: dst},
	}
	rev := []Hop{
		{TpID: uuid.New(), From: dst, To: inter},
		{TpID: uuid.New(), From: inter, To: src},
	}
	return BidirectionalRoute{
		Desc:    NewRouteDescriptor(src, dst, 1, 2),
		Forward: fwd,
		Reverse: rev,
	}
}

func batchOf(t *testing.T, n int) (BidirectionalRouteBatch, cipher.PubKey, cipher.PubKey) {
	t.Helper()
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	b := BidirectionalRouteBatch{}
	for i := 0; i < n; i++ {
		inter, _ := cipher.GenerateKeyPair()
		b.Routes = append(b.Routes, BatchRouteRequest{ID: uint32(i), Route: twoHopRoute(src, inter, dst)}) //nolint:gosec
	}
	return b, src, dst
}

func TestBatchCheckAcceptsAWellFormedBatch(t *testing.T) {
	b, src, dst := batchOf(t, 4)
	require.NoError(t, b.Check())
	require.Equal(t, src, b.Src())
	require.Equal(t, dst, b.Dst())
	// src + dst + 4 distinct intermediates.
	require.Equal(t, 6, b.DistinctHopPKs())
	// Forward and reverse per member, in member order.
	require.Len(t, b.Hops(), 8)
}

func TestBatchCheckRejects(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		var b BidirectionalRouteBatch
		require.ErrorIs(t, b.Check(), ErrBatchEmpty)
	})

	t.Run("too large", func(t *testing.T) {
		b, _, _ := batchOf(t, MaxBatchRoutes+1)
		require.ErrorIs(t, b.Check(), ErrBatchTooLarge)
	})

	t.Run("duplicate correlation id", func(t *testing.T) {
		b, _, _ := batchOf(t, 2)
		b.Routes[1].ID = b.Routes[0].ID
		require.ErrorIs(t, b.Check(), ErrBatchDuplicateID)
	})

	t.Run("mixed endpoints", func(t *testing.T) {
		b, _, _ := batchOf(t, 2)
		other, _ := cipher.GenerateKeyPair()
		inter, _ := cipher.GenerateKeyPair()
		b.Routes[1].Route = twoHopRoute(other, inter, b.Dst())
		require.ErrorIs(t, b.Check(), ErrBatchMixedEndpoints)
	})

	t.Run("invalid member", func(t *testing.T) {
		b, _, _ := batchOf(t, 2)
		b.Routes[1].Route.Forward = nil
		require.ErrorIs(t, b.Check(), ErrBiRouteHasNoForwardHops)
	})
}

// The batch and its reply cross the setup RPC, whose codec is gob. A field the
// codec cannot carry would fail silently at runtime on a live setup node, so
// assert the round trip here.
func TestBatchEncodeDecodeRoundTrip(t *testing.T) {
	b, _, _ := batchOf(t, 3)

	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(b))
	var got BidirectionalRouteBatch
	require.NoError(t, gob.NewDecoder(&buf).Decode(&got))
	require.NoError(t, got.Check())
	require.Len(t, got.Routes, 3)
	for i := range b.Routes {
		require.Equal(t, b.Routes[i].ID, got.Routes[i].ID)
		require.Equal(t, b.Routes[i].Route.Desc, got.Routes[i].Route.Desc)
		require.Equal(t, b.Routes[i].Route.Forward, got.Routes[i].Route.Forward)
		require.Equal(t, b.Routes[i].Route.Reverse, got.Routes[i].Route.Reverse)
	}

	// The reply's partial-success shape must survive too: a result carrying an
	// error string and one carrying rules, side by side.
	reply := BidirectionalRouteBatchReply{Results: []BatchRouteResult{
		{ID: 0, Rules: EdgeRules{Desc: b.Routes[0].Route.Desc}},
		{ID: 1, Error: "intermediary rules on peer: refused"},
	}}
	buf.Reset()
	require.NoError(t, gob.NewEncoder(&buf).Encode(reply))
	var gotReply BidirectionalRouteBatchReply
	require.NoError(t, gob.NewDecoder(&buf).Decode(&gotReply))
	require.Len(t, gotReply.Results, 2)
	require.False(t, gotReply.Results[0].Failed())
	require.True(t, gotReply.Results[1].Failed())
	require.Equal(t, "intermediary rules on peer: refused", gotReply.Results[1].Error)
}

// Members sharing an intermediate collapse in the distinct-hop count, which is
// exactly where the per-hop coalescing saves the most.
func TestBatchDistinctHopPKsCountsSharedIntermediatesOnce(t *testing.T) {
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	inter, _ := cipher.GenerateKeyPair()
	b := BidirectionalRouteBatch{Routes: []BatchRouteRequest{
		{ID: 0, Route: twoHopRoute(src, inter, dst)},
		{ID: 1, Route: twoHopRoute(src, inter, dst)},
		{ID: 2, Route: twoHopRoute(src, inter, dst)},
	}}
	require.NoError(t, b.Check())
	require.Equal(t, 3, b.DistinctHopPKs(), "src, dst and the one shared intermediate")
}
