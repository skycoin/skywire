//go:build !tinygo

// Package appserver pkg/app/appserver/proc_ingress_rpc_native.go c2-vis-appsvc
package appserver

import (
	rpc "github.com/skycoin/skywire/pkg/gobrpc"
)

// registerIngressRPC registers the ingress gateway on the native build using
// gobrpc's (stdlib net/rpc) reflection-based RegisterName. See the TinyGo
// variant for why the browser build needs a reflection-free registration.
func registerIngressRPC(s *rpc.Server, name string, gw *RPCIngressGateway) error {
	return s.RegisterName(name, gw)
}
