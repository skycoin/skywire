//go:build tinygo

// Package router pkg/router/router_setup_rpc_tinygo.go
package router

import (
	"encoding/gob"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// registerSetupRPC registers the setup RPC gateway using gobrpc's reflection-free
// HandleFunc path. TinyGo's runtime reflect can't enumerate or invoke methods
// (reflect.Type.Method hangs; reflect.Value.Call is unsupported), so net/rpc-style
// Register deadlocks — see pkg/gobrpc/gobimpl. Each handler decodes its argument
// (matching the gateway method's arg type) and returns the reply value; the wire
// format is identical to net/rpc, so a native router peer interoperates.
func (r *router) registerSetupRPC(mLog *logging.MasterLogger) error {
	gw := NewRPCGateway(r, mLog)

	r.rpcSrv.HandleFunc(RPCName+".AddEdgeRules", func(dec *gob.Decoder) (interface{}, error) {
		var rules routing.EdgeRules
		if err := dec.Decode(&rules); err != nil {
			return nil, err
		}
		var ok bool
		err := gw.AddEdgeRules(rules, &ok)
		return ok, err
	})

	r.rpcSrv.HandleFunc(RPCName+".AddIntermediaryRules", func(dec *gob.Decoder) (interface{}, error) {
		var rules []routing.Rule
		if err := dec.Decode(&rules); err != nil {
			return nil, err
		}
		var ok bool
		err := gw.AddIntermediaryRules(rules, &ok)
		return ok, err
	})

	r.rpcSrv.HandleFunc(RPCName+".ReserveIDs", func(dec *gob.Decoder) (interface{}, error) {
		var n uint8
		if err := dec.Decode(&n); err != nil {
			return nil, err
		}
		var ids []routing.RouteID
		err := gw.ReserveIDs(n, &ids)
		return ids, err
	})

	r.rpcSrv.HandleFunc(RPCName+".DelRules", func(dec *gob.Decoder) (interface{}, error) {
		var ids []routing.RouteID
		if err := dec.Decode(&ids); err != nil {
			return nil, err
		}
		var ok bool
		err := gw.DelRules(ids, &ok)
		return ok, err
	})

	return nil
}
