package setup

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/skycoin/dmsg/cipher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
)

// We check the contents of 'idReserver.rec' is as expected after calling 'NewIDReserver'.
// We assume that all dials by the dialer are successful.
func TestNewIDReserver(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	pkC, _ := cipher.GenerateKeyPair()

	type testCase struct {
		paths    [][]routing.Hop         // test input
		expRec   map[cipher.PubKey]uint8 // expected 'idReserver.rec' result
		expTotal int                     // expected 'idReserver.total' result
	}

	testCases := []testCase{
		{
			paths:    nil,
			expRec:   map[cipher.PubKey]uint8{},
			expTotal: 0,
		},
		{
			paths:    [][]routing.Hop{makeHops(pkA, pkB), makeHops(pkB, pkA)},
			expRec:   map[cipher.PubKey]uint8{pkA: 2, pkB: 2},
			expTotal: 4,
		},
		{
			paths:    [][]routing.Hop{makeHops(pkA, pkB, pkC), makeHops(pkC, pkA)},
			expRec:   map[cipher.PubKey]uint8{pkA: 2, pkB: 1, pkC: 2},
			expTotal: 5,
		},
		{
			paths:    [][]routing.Hop{makeHops(pkA, pkB, pkC), makeHops(pkC, pkB, pkA)},
			expRec:   map[cipher.PubKey]uint8{pkA: 2, pkB: 2, pkC: 2},
			expTotal: 6,
		},
	}

	for i, tc := range testCases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			// arrange
			dialer := newMockDialer(t, nil)

			// act
			rtIDR, err := NewIDReserver(context.TODO(), dialer, tc.paths)
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, rtIDR.Close()) })

			// assert
			v := rtIDR.(*idReserver)
			assert.Equal(t, tc.expTotal, v.total)
			assert.Equal(t, tc.expRec, v.rec)
		})
	}
}

func TestIdReserver_ReserveIDs(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	pkC, _ := cipher.GenerateKeyPair()

	// timeout given for all calls to .ReserveIDs
	// this is passed with a context with deadline
	timeout := time.Second

	type testCase struct {
		testName string                        // test name
		routers  map[cipher.PubKey]interface{} // arrange: map of mock router gateways
		paths    [][]routing.Hop               // arrange: idReserver input
		expErr   error                         // assert: expected error
	}

	makeRun := func(tc testCase) func(t *testing.T) {
		return func(t *testing.T) {
			// arrange
			dialer := newMockDialer(t, tc.routers)

			rtIDR, err := NewIDReserver(context.TODO(), dialer, tc.paths)
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, rtIDR.Close()) })

			ctx, cancel := context.WithDeadline(context.TODO(), time.Now().Add(timeout))
			defer cancel()

			// act
			err = rtIDR.ReserveIDs(ctx)

			if tc.expErr != nil {
				// assert (expected error)
				assert.EqualError(t, errors.Unwrap(err), context.DeadlineExceeded.Error())
			} else {
				// assert (no expected error)
				checkIDReserver(t, rtIDR.(*idReserver))
			}
		}
	}

	// .ReserveIDs should correctly reserve IDs if all remote routers are functional.
	t.Run("correctly_reserve_rtIDs", func(t *testing.T) {
		testCases := []testCase{
			{
				testName: "fwd1_rev1",
				routers: map[cipher.PubKey]interface{}{
					pkA: &mockGatewayForDialer{},
					pkC: &mockGatewayForDialer{},
				},
				paths:  [][]routing.Hop{makeHops(pkA, pkC), makeHops(pkC, pkA)},
				expErr: nil,
			},
			{
				testName: "fwd2_rev2",
				routers: map[cipher.PubKey]interface{}{
					pkA: &mockGatewayForDialer{},
					pkB: &mockGatewayForDialer{},
					pkC: &mockGatewayForDialer{},
				},
				paths:  [][]routing.Hop{makeHops(pkA, pkB, pkC), makeHops(pkC, pkB, pkA)},
				expErr: nil,
			},
			{
				testName: "fwd1_rev2",
				routers: map[cipher.PubKey]interface{}{
					pkA: &mockGatewayForDialer{},
					pkB: &mockGatewayForDialer{},
					pkC: &mockGatewayForDialer{},
				},
				paths:  [][]routing.Hop{makeHops(pkA, pkC), makeHops(pkC, pkB, pkA)},
				expErr: nil,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.testName, makeRun(tc))
		}
	})

	// Calling .ReserveIDs should never hang indefinitely if we set context with timeout.
	// We set this by providing a context.Context with a timeout.
	// Hence, .ReserveIDs should return context.DeadlineExceeded when the timeout triggers.
	// Any other errors, or further delays is considered a failure.
	t.Run("no_hangs_with_ctx_timeout", func(t *testing.T) {
		testCases := []testCase{
			{
				testName: "all_routers_hang",
				routers: map[cipher.PubKey]interface{}{
					pkA: &mockGatewayForDialer{hangDuration: time.Second * 5},
					pkC: &mockGatewayForDialer{hangDuration: time.Second * 5},
				},
				paths:  [][]routing.Hop{makeHops(pkA, pkC), makeHops(pkC, pkA)},
				expErr: context.DeadlineExceeded,
			},
			{
				testName: "intermediary_router_hangs",
				routers: map[cipher.PubKey]interface{}{
					pkA: &mockGatewayForDialer{},
					pkB: &mockGatewayForDialer{hangDuration: time.Second * 5},
					pkC: &mockGatewayForDialer{},
				},
				paths:  [][]routing.Hop{makeHops(pkA, pkB, pkC), makeHops(pkC, pkB, pkA)},
				expErr: context.DeadlineExceeded,
			},
			{
				testName: "initiating_router_hangs",
				routers: map[cipher.PubKey]interface{}{
					pkA: &mockGatewayForDialer{hangDuration: time.Second * 5},
					pkB: &mockGatewayForDialer{},
					pkC: &mockGatewayForDialer{},
				},
				paths:  [][]routing.Hop{makeHops(pkA, pkC), makeHops(pkC, pkB, pkA)},
				expErr: context.DeadlineExceeded,
			},
			{
				testName: "responding_router_hangs",
				routers: map[cipher.PubKey]interface{}{
					pkA: &mockGatewayForDialer{},
					pkB: &mockGatewayForDialer{},
					pkC: &mockGatewayForDialer{hangDuration: time.Second * 5},
				},
				paths:  [][]routing.Hop{makeHops(pkA, pkB, pkC), makeHops(pkC, pkB, pkA)},
				expErr: context.DeadlineExceeded,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.testName, makeRun(tc))
		}
	})
}

// makes a slice of hops from pks
func makeHops(pks ...cipher.PubKey) []routing.Hop {
	hops := make([]routing.Hop, len(pks)-1)
	for i, pk := range pks[:len(pks)-1] {
		hops[i] = routing.Hop{
			TpID: uuid.New(),
			From: pk,
			To:   pks[i+1],
		}
	}
	return hops
}

// ensures that the internal idReserver.rec and idReserver.ids match up
func checkIDReserver(t *testing.T, rtIDR *idReserver) {
	assert.Equal(t, len(rtIDR.rec), len(rtIDR.ids))

	// ensure values of .ids are okay
	for pk, rec := range rtIDR.rec {
		ids, ok := rtIDR.ids[pk]

		assert.True(t, ok)
		assert.Len(t, ids, int(rec))

		// ensure there are no duplicates in 'ids'
		idMap := make(map[routing.RouteID]struct{}, len(ids))
		for _, id := range ids {
			idMap[id] = struct{}{}
		}
		assert.Len(t, idMap, len(ids))
	}
}
