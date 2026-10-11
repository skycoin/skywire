package setup

import (
	"testing"

	rpc "github.com/0magnet/gobrpc"

	"github.com/skycoin/skywire/internal/rpctest"
)

func TestTransportGatewayRPCHandlers(t *testing.T) {
	gw := &TransportGateway{}
	rpctest.CheckHandlers(t, gw, func(s *rpc.Server, name string) error { return registerTransportGateway(s, name, gw) })
}
