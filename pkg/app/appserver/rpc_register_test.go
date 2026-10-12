package appserver

import (
	"testing"

	rpc "github.com/0magnet/gobrpc"

	"github.com/skycoin/skywire/internal/rpctest"
	"github.com/skycoin/skywire/pkg/proxystatus"
)

func TestIngressRPCHandlers(t *testing.T) {
	gw := NewRPCGateway(nil, nil)
	reg := func(s *rpc.Server, name string) error { return registerIngressRPC(s, name, gw) }
	rpctest.CheckHandlers(t, gw, reg)
	rpctest.CompareCall(t, gw, reg, "ProxyStatus", &struct{}{}, func() any { return new(proxystatus.Snapshot) })
	id := uint16(7)
	rpctest.CompareCall(t, gw, reg, "CloseConn", &id, func() any { return new(struct{}) })
}
