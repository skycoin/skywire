package router

import (
	"errors"
	"testing"

	rpc "github.com/0magnet/gobrpc"

	"github.com/skycoin/skywire/internal/rpctest"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// rpcTestRouter answers ReserveKeys for the registration test.
type rpcTestRouter struct{ Router }

func (rpcTestRouter) ReserveKeys(n int) ([]routing.RouteID, error) {
	if n == 0 {
		return nil, errors.New("nothing to reserve")
	}
	ids := make([]routing.RouteID, n)
	for i := range ids {
		ids[i] = routing.RouteID(100 + i)
	}
	return ids, nil
}

func TestRouterRPCHandlers(t *testing.T) {
	gw := NewRPCGateway(rpcTestRouter{}, logging.NewMasterLogger(), false)
	reg := func(s *rpc.Server, name string) error { return registerRPCGateway(s, name, gw) }
	rpctest.CheckHandlers(t, gw, reg)

	got := rpctest.CompareCall(t, gw, reg, "ReserveIDs", uint8(2), func() any { return new([]routing.RouteID) })
	if ids := *got.(*[]routing.RouteID); len(ids) != 2 || ids[1] != 101 {
		t.Errorf("ReserveIDs = %v", ids)
	}
	rpctest.CompareCall(t, gw, reg, "ReserveIDs", uint8(0), func() any { return new([]routing.RouteID) })

	sgw := &SetupRPCGateway{}
	sreg := func(s *rpc.Server, name string) error { return RegisterSetupRPCGateway(s, name, sgw) }
	rpctest.CheckHandlers(t, sgw, sreg)
	rpctest.CompareCall(t, sgw, sreg, "Capabilities", &CapabilitiesArgs{}, func() any { return new(CapabilitiesReply) })

	qgw := &TransportQueryRPCGateway{}
	rpctest.CheckHandlers(t, qgw, func(s *rpc.Server, name string) error { return registerTransportQueryRPCGateway(s, name, qgw) })
}
