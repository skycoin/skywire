package api

import (
	"testing"

	rpc "github.com/0magnet/gobrpc"

	"github.com/skycoin/skywire/internal/rpctest"
)

func TestSetupRPCGatewayHandlers(t *testing.T) {
	gw := &SetupRPCGateway{}
	rpctest.CheckHandlers(t, gw, func(s *rpc.Server, name string) error { return registerSetupRPCGateway(s, name, gw) })
}
