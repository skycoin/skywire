//go:build tinygo

// Package router pkg/router/direct_setup_rpc_tinygo.go c2-net-routing
package router

import (
	"encoding/gob"

	rpc "github.com/0magnet/gobrpc"

	"github.com/skycoin/skywire/pkg/routing"
)

// registerDirectSetupRPC registers a direct peer's setup gateway with
// explicit handlers, as registerSetupRPC does on TinyGo.
func registerDirectSetupRPC(srv *rpc.Server, gw *directSetupGateway) error {
	srv.HandleFunc(RPCName+".ReserveIDs", func(dec *gob.Decoder) (interface{}, error) {
		var n uint8
		if err := dec.Decode(&n); err != nil {
			return nil, err
		}
		var ids []routing.RouteID
		err := gw.ReserveIDs(n, &ids)
		return ids, err
	})
	srv.HandleFunc(RPCName+".AddEdgeRules", func(dec *gob.Decoder) (interface{}, error) {
		var rules routing.EdgeRules
		if err := dec.Decode(&rules); err != nil {
			return nil, err
		}
		var ok bool
		err := gw.AddEdgeRules(rules, &ok)
		return ok, err
	})
	return nil
}
