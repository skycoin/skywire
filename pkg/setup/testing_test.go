// Package setup pkg/setup/testing_test.go
package setup

import (
	"context"
	"fmt"
	"net"
	"net/rpc"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/routerclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// creates a mock dialer
func newMockDialer(t *testing.T, gateways map[cipher.PubKey]interface{}) network.Dialer {
	newRPCConn := func(gw interface{}) net.Conn {
		connC, connS := net.Pipe()
		t.Cleanup(func() {
			assert.NoError(t, connC.Close())
			assert.NoError(t, connS.Close())
		})

		rpcS := rpc.NewServer()
		require.NoError(t, rpcS.RegisterName(routerclient.RPCName, gw))
		go rpcS.ServeConn(connS)

		return connC
	}

	if gateways == nil {
		conn := newRPCConn(new(mockGatewayForDialer))
		dialer := new(network.MockDialer)
		dialer.On("Dial", mock.Anything, mock.Anything, mock.Anything).Return(conn, nil)
		return dialer
	}

	dialer := make(mockDialer, len(gateways))
	for pk, gw := range gateways {
		dialer[pk] = newRPCConn(gw)
	}
	return dialer
}

type mockDialer map[cipher.PubKey]net.Conn

func (d mockDialer) Type() string { return string(network.DMSG) }

func (d mockDialer) Dial(_ context.Context, remote cipher.PubKey, _ uint16) (net.Conn, error) {
	conn, ok := d[remote]
	if !ok {
		return nil, fmt.Errorf("cannot dial to given pk %s", remote)
	}
	return conn, nil
}

// mockGatewayForDialer is the default mock router.RPCGateway for newMockDialer.
// It reserves route IDs sequentially for each .ReserveIDs call.
// If hangDuration is > 0, calling .ReserveIDS would hang for the given duration before returning.
type mockGatewayForDialer struct {
	hangDuration time.Duration
	nextID       uint32
}

func (gw *mockGatewayForDialer) ReserveIDs(n uint8, routeIDs *[]routing.RouteID) error {
	if gw.hangDuration != 0 {
		time.Sleep(gw.hangDuration)
	}

	out := make([]routing.RouteID, n)
	for i := 0; i < int(n); i++ {
		out[i] = routing.RouteID(atomic.AddUint32(&gw.nextID, 1))
	}
	*routeIDs = out
	return nil
}

// create a mock id reserver
func newMockReserver(t *testing.T, gateways map[cipher.PubKey]interface{}) IDReserver {
	rtIDR := new(MockIDReserver)

	handlePK := func(pk, gw interface{}) {
		connC, connS := net.Pipe()
		t.Cleanup(func() {
			assert.NoError(t, connC.Close())
			assert.NoError(t, connS.Close())
		})

		rpcS := rpc.NewServer()
		require.NoError(t, rpcS.RegisterName(routerclient.RPCName, gw))
		go rpcS.ServeConn(connS)

		pkRaw, _ := pk.(cipher.PubKey)
		rc := routerclient.NewClientFromRaw(connC, pkRaw)
		rtIDR.On("Client", pk).Return(rc)
		rtIDR.On("PopID", mock.Anything).Return(routing.RouteID(1), true)
	}

	if gateways == nil {
		handlePK(mock.Anything, new(mockGatewayForReserver))
	} else {
		for pk, gw := range gateways {
			handlePK(pk, gw)
		}
	}

	return rtIDR
}

// mockGatewayForReserver is the default mock router.RPCGateway for newMockReserver.
// It pretends to successfully trigger .AddIntermediaryRules.
// If handDuration is set, calling .ReserveIDs should hang for given duration before returning
type mockGatewayForReserver struct {
	hangDuration time.Duration
}

func (gw *mockGatewayForReserver) AddIntermediaryRules(_ []routing.Rule, ok *bool) error {
	if gw.hangDuration > 0 {
		time.Sleep(gw.hangDuration)
	}
	*ok = true
	return nil
}
