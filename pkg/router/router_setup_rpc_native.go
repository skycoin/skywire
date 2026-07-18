//go:build !tinygo

// Package router pkg/router/router_setup_rpc_native.go c2-net-routing
package router

import "github.com/skycoin/skywire/pkg/logging"

// registerSetupRPC registers the setup RPC gateway via net/rpc-style reflection
// (gobrpc.Server is net/rpc.Server on native builds). The TinyGo build registers
// explicit handlers instead — see router_setup_rpc_tinygo.go.
func (r *router) registerSetupRPC(mLog *logging.MasterLogger) error {
	return r.rpcSrv.Register(NewRPCGateway(r, mLog))
}
