// Package setup pkg/setup/id_reserver_test.go
package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	types "github.com/skycoin/skywire/pkg/transport/types"
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
			rtIDR, err := NewIDReserver(context.TODO(), dialer, nil, tc.paths)
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

			rtIDR, err := NewIDReserver(context.TODO(), dialer, nil, tc.paths)
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

// errReserveGateway is a mock router RPCGateway whose ReserveIDs always fails.
type errReserveGateway struct{}

func (errReserveGateway) ReserveIDs(_ uint8, _ *[]routing.RouteID) error {
	return errors.New("mock: reserve failed")
}

// TestIdReserver_ReserveIDs_NoSiblingCancelOnFailure is a regression test for the
// route-setup "RST race": one hop's reservation failure must NOT cancel the
// shared context, because that tore down the (pooled) DMSG streams of the other
// in-flight hops mid-call, RST-ing healthy intermediates and failing the whole
// route. Here hop A fails immediately while hop C is slow but healthy; C must
// still reserve its ID rather than being canceled by A's failure.
func TestIdReserver_ReserveIDs_NoSiblingCancelOnFailure(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair() // fails fast
	pkC, _ := cipher.GenerateKeyPair() // slow but healthy

	routers := map[cipher.PubKey]interface{}{
		pkA: errReserveGateway{},
		pkC: &mockGatewayForDialer{hangDuration: 150 * time.Millisecond},
	}
	dialer := newMockDialer(t, routers)

	rtIDR, err := NewIDReserver(context.TODO(), dialer, nil, [][]routing.Hop{makeHops(pkA, pkC)})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, rtIDR.Close()) })

	ctx, cancel := context.WithTimeout(context.TODO(), 2*time.Second)
	defer cancel()

	err = rtIDR.ReserveIDs(ctx)

	// A failed, so the overall reservation returns an error — but it must be
	// A's reservation error, not a context cancellation that leaked into C.
	require.Error(t, err)
	assert.NotContains(t, err.Error(), context.Canceled.Error())

	// The key assertion: C's reservation completed and was recorded, proving
	// A's failure did not cancel/RST the healthy sibling.
	idr := rtIDR.(*idReserver)
	idr.mx.Lock()
	cIDs := idr.ids[pkC]
	idr.mx.Unlock()
	assert.Len(t, cIDs, 1, "healthy sibling C should reserve its ID despite A failing")
}

// shutdownGateway is a mock router.RPCGateway whose ReserveIDs returns net/rpc's
// "connection is shut down" — the error a stale pooled connection produces.
type shutdownGateway struct{}

func (shutdownGateway) ReserveIDs(_ uint8, _ *[]routing.RouteID) error {
	return errors.New("connection is shut down")
}

// flakyReserveDialer serves a shutdownGateway on the FIRST dial to each pk and a
// working mockGatewayForDialer on every subsequent dial — modeling a pooled
// connection that is dead on first use but reachable on a fresh re-dial.
type flakyReserveDialer struct {
	t  *testing.T
	mu sync.Mutex
	n  map[cipher.PubKey]int
}

func (d *flakyReserveDialer) Type() string                                      { return string(types.DMSG) }
func (d *flakyReserveDialer) Probe(context.Context, cipher.PubKey, uint16) bool { return true }

func (d *flakyReserveDialer) Dial(_ context.Context, pk cipher.PubKey, _ uint16) (net.Conn, error) {
	d.mu.Lock()
	k := d.n[pk]
	d.n[pk]++
	d.mu.Unlock()

	var gw interface{} = &mockGatewayForDialer{}
	if k == 0 {
		gw = shutdownGateway{}
	}
	connC, connS := net.Pipe()
	rpcS := rpc.NewServer()
	require.NoError(d.t, rpcS.RegisterName(RPCName, gw))
	go rpcS.ServeConn(connS)
	d.t.Cleanup(func() {
		assert.NoError(d.t, connC.Close())
		assert.NoError(d.t, connS.Close())
	})
	return connC, nil
}

// TestIdReserver_ReserveIDs_RetriesStalePooledConn covers the fix for the #1 live
// route-setup failure (id_reservation, ~59% on a real RSN): a pooled connection
// returning "connection is shut down" must be re-dialed fresh and the
// reservation retried, instead of failing the whole route. Without the retry,
// ReserveIDs returns that error directly.
func TestIdReserver_ReserveIDs_RetriesStalePooledConn(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	d := &flakyReserveDialer{t: t, n: make(map[cipher.PubKey]int)}

	rtIDR, err := NewIDReserver(context.TODO(), d, nil, [][]routing.Hop{makeHops(pkA, pkB)})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, rtIDR.Close()) })

	ctx, cancel := context.WithTimeout(context.TODO(), 3*time.Second)
	defer cancel()

	// The first pooled conn to each hop is dead → "connection is shut down"; the
	// reserver must re-dial fresh and succeed.
	require.NoError(t, rtIDR.ReserveIDs(ctx))

	// Each hop was dialed at least twice: the initial dead conn + one re-dial.
	d.mu.Lock()
	defer d.mu.Unlock()
	assert.GreaterOrEqual(t, d.n[pkA], 2, "hop A should have been re-dialed")
	assert.GreaterOrEqual(t, d.n[pkB], 2, "hop B should have been re-dialed")
}

// TestIsReserveResetErr covers the Fix-#1 decision logic: a reserve call that
// failed because its stream was reset/closed mid-flight (raw io.EOF from
// net/rpc's reader, io.ErrUnexpectedEOF, or net.ErrClosed) must be treated as
// retryable, not just the already-dead "connection is shut down" case. This is
// the dominant mux aux-leg setup churn ("reserve routeID from <pk> failed: EOF")
// that previously fell through un-retried.
func TestIsReserveResetErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"shutdown-string", errors.New("connection is shut down"), true},
		{"raw-eof", io.EOF, true},
		{"wrapped-eof", fmt.Errorf("reserve: %w", io.EOF), true},
		{"unexpected-eof", io.ErrUnexpectedEOF, true},
		{"net-closed", net.ErrClosed, true},
		{"wrapped-net-closed", fmt.Errorf("dial: %w", net.ErrClosed), true},
		{"unrelated", errors.New("route not found"), false},
		{"deadline", context.DeadlineExceeded, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, isReserveResetErr(c.err))
		})
	}
}
