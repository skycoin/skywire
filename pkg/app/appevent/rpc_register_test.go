package appevent

import (
	"testing"

	rpc "github.com/0magnet/gobrpc"

	"github.com/skycoin/skywire/internal/rpctest"
)

func TestEgressRPCHandlers(t *testing.T) {
	gw := NewRPCGateway(nil, NewSubscriber())
	rpctest.CheckHandlers(t, gw, func(s *rpc.Server, name string) error { return registerRPCGateway(s, name, gw) })
}
