//go:build !tinygo

// Package router pkg/router/direct_setup_rpc_native.go c2-net-routing
package router

import rpc "github.com/0magnet/gobrpc"

// registerDirectSetupRPC registers a direct peer's setup gateway under the
// name a setup node's gateway uses, so the router client calls it unchanged.
func registerDirectSetupRPC(srv *rpc.Server, gw *directSetupGateway) error {
	return srv.RegisterName(RPCName, gw)
}
