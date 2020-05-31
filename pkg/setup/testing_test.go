package setup

import (
	"net"
	"net/rpc"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/SkycoinProject/skywire-mainnet/pkg/router/routerclient"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
)

// creates a mock dialer
func newMockDialer(t *testing.T, gateways map[cipher.PubKey]*mockGatewayForDialer) snet.Dialer {
	dialer := new(snet.MockDialer)

	handlePK := func(pk, gw interface{}) {
		connC, connS := net.Pipe()
		t.Cleanup(func() {
			assert.NoError(t, connC.Close())
			assert.NoError(t, connS.Close())
		})

		rpcS := rpc.NewServer()
		require.NoError(t, rpcS.RegisterName(routerclient.RPCName, gw))
		go rpcS.ServeConn(connS)

		dialer.On("Dial", mock.Anything, pk, mock.Anything).Return(connC, nil)
	}

	if gateways == nil {
		handlePK(mock.Anything, new(mockGatewayForDialer))
	} else {
		for pk, gw := range gateways {
			handlePK(pk, gw)
		}
	}
	return dialer
}

type mockGatewayForDialer struct {
	hangDuration time.Duration // if set, calling .ReserveIDs should hang for given duration before returning
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
func newMockReserver(t *testing.T, gateways map[cipher.PubKey]*mockGatewayForReserver) IDReserver {
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

type mockGatewayForReserver struct {
	hangDuration time.Duration // if set, calling .ReserveIDs should hang for given duration before returning
}

func (gw *mockGatewayForReserver) AddIntermediaryRules(_ []routing.Rule, ok *bool) error {
	if gw.hangDuration > 0 {
		time.Sleep(gw.hangDuration)
	}
	*ok = true
	return nil
}
