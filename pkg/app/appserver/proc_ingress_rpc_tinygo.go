//go:build tinygo

// Package appserver pkg/app/appserver/proc_ingress_rpc_tinygo.go c2-vis-appsvc
package appserver

import (
	"encoding/gob"

	"github.com/skycoin/skywire/pkg/app/appnet"
	rpc "github.com/skycoin/skywire/pkg/gobrpc"
	"github.com/skycoin/skywire/pkg/routing"
)

// registerIngressRPC registers each RPCIngressGateway method as a reflection-free
// gobrpc HandleFunc. TinyGo's runtime reflect can't enumerate methods
// (reflect.Type.Method) or invoke them (reflect.Value.Call), so gobrpc's
// reflection-based RegisterName panics under TinyGo — wedging the in-process app
// the browser wasm-visor launches (skychat etc.). Each handler decodes exactly
// one gob argument value and calls the concrete method; the wire format is
// identical to the reflection path, so the app-side gobrpc client is unchanged.
// This mirrors how gobrpc registers the visor edge RPC on TinyGo.
func registerIngressRPC(s *rpc.Server, name string, gw *RPCIngressGateway) error {
	h := func(method string, fn rpc.HandlerFunc) { s.HandleFunc(name+"."+method, fn) }

	h("SetDetailedStatus", func(dec *gob.Decoder) (interface{}, error) {
		var a string
		if err := dec.Decode(&a); err != nil {
			return nil, err
		}
		var r struct{}
		return &r, gw.SetDetailedStatus(&a, &r)
	})
	h("SetOTP", func(dec *gob.Decoder) (interface{}, error) {
		var a string
		if err := dec.Decode(&a); err != nil {
			return nil, err
		}
		var r struct{}
		return &r, gw.SetOTP(&a, &r)
	})
	h("SetConnectionDuration", func(dec *gob.Decoder) (interface{}, error) {
		var a int64
		if err := dec.Decode(&a); err != nil {
			return nil, err
		}
		var r struct{}
		return &r, gw.SetConnectionDuration(a, &r)
	})
	h("SetError", func(dec *gob.Decoder) (interface{}, error) {
		var a string
		if err := dec.Decode(&a); err != nil {
			return nil, err
		}
		var r struct{}
		return &r, gw.SetError(&a, &r)
	})
	h("SetAppPort", func(dec *gob.Decoder) (interface{}, error) {
		var a routing.Port
		if err := dec.Decode(&a); err != nil {
			return nil, err
		}
		var r struct{}
		return &r, gw.SetAppPort(a, &r)
	})
	h("Dial", func(dec *gob.Decoder) (interface{}, error) {
		var a appnet.Addr
		if err := dec.Decode(&a); err != nil {
			return nil, err
		}
		var r DialResp
		return &r, gw.Dial(&a, &r)
	})
	h("DialWithOptions", func(dec *gob.Decoder) (interface{}, error) {
		var a DialOptionsReq
		if err := dec.Decode(&a); err != nil {
			return nil, err
		}
		var r DialResp
		return &r, gw.DialWithOptions(&a, &r)
	})
	h("Listen", func(dec *gob.Decoder) (interface{}, error) {
		var a appnet.Addr
		if err := dec.Decode(&a); err != nil {
			return nil, err
		}
		var r uint16
		return &r, gw.Listen(&a, &r)
	})
	h("Accept", func(dec *gob.Decoder) (interface{}, error) {
		var a uint16
		if err := dec.Decode(&a); err != nil {
			return nil, err
		}
		var r AcceptResp
		return &r, gw.Accept(&a, &r)
	})
	h("Write", func(dec *gob.Decoder) (interface{}, error) {
		var a WriteReq
		if err := dec.Decode(&a); err != nil {
			return nil, err
		}
		var r WriteResp
		return &r, gw.Write(&a, &r)
	})
	h("Read", func(dec *gob.Decoder) (interface{}, error) {
		var a ReadReq
		if err := dec.Decode(&a); err != nil {
			return nil, err
		}
		var r ReadResp
		return &r, gw.Read(&a, &r)
	})
	h("CloseConn", func(dec *gob.Decoder) (interface{}, error) {
		var a uint16
		if err := dec.Decode(&a); err != nil {
			return nil, err
		}
		var r struct{}
		return &r, gw.CloseConn(&a, &r)
	})
	h("CloseListener", func(dec *gob.Decoder) (interface{}, error) {
		var a uint16
		if err := dec.Decode(&a); err != nil {
			return nil, err
		}
		var r struct{}
		return &r, gw.CloseListener(&a, &r)
	})
	h("SetDeadline", func(dec *gob.Decoder) (interface{}, error) {
		var a DeadlineReq
		if err := dec.Decode(&a); err != nil {
			return nil, err
		}
		var r struct{}
		return &r, gw.SetDeadline(&a, &r)
	})
	h("SetReadDeadline", func(dec *gob.Decoder) (interface{}, error) {
		var a DeadlineReq
		if err := dec.Decode(&a); err != nil {
			return nil, err
		}
		var r struct{}
		return &r, gw.SetReadDeadline(&a, &r)
	})
	h("SetWriteDeadline", func(dec *gob.Decoder) (interface{}, error) {
		var a DeadlineReq
		if err := dec.Decode(&a); err != nil {
			return nil, err
		}
		var r struct{}
		return &r, gw.SetWriteDeadline(&a, &r)
	})
	return nil
}
