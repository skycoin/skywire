package node

import (
	"testing"

	rpc "github.com/0magnet/gobrpc"

	"github.com/skycoin/skywire/internal/rpctest"
)

func TestNodeRPCHandlers(t *testing.T) {
	n := &RPC{}
	rpctest.CheckHandlers(t, n, func(s *rpc.Server, name string) error { return registerNodeRPC(s, name, n) })
	tcp := &TCPRPC{}
	rpctest.CheckHandlers(t, tcp, func(s *rpc.Server, name string) error { return registerTCPRPC(s, name, tcp) })
	udp := &UDPRPC{}
	rpctest.CheckHandlers(t, udp, func(s *rpc.Server, name string) error { return registerUDPRPC(s, name, udp) })
	root := &RootRPC{}
	rpctest.CheckHandlers(t, root, func(s *rpc.Server, name string) error { return registerRootRPC(s, name, root) })
	dm := &DMSGRPC{}
	rpctest.CheckHandlers(t, dm, func(s *rpc.Server, name string) error { return registerDMSGRPC(s, name, dm) })
}
