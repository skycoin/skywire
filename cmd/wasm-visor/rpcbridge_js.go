//go:build js && wasm

// Package main cmd/wasm-visor/rpcbridge_js.go c3-vis-wasm
//
// A browser tab can't open a TCP listener, so it dials the host bridge over a
// WebSocket; the host (cmd/dmsg-wasm/serve.go) fronts it with a local
// 127.0.0.1:<port> that `skywire --rpc <port> visor …` targets. See pkg/wasmrpc.
package main

import (
	"context"
	"io"
	"syscall/js"

	"github.com/coder/websocket"

	"github.com/skycoin/skywire/pkg/wasmhv"
	"github.com/skycoin/skywire/pkg/wasmrpc"
)

// jsServeRPC(wsURL) opens a WebSocket to the host RPC bridge and serves this
// tab's visor RPC (the app-visor.* gateway) over it, so the ordinary skywire CLI
// can drive this specific tab. Idempotent-ish: each call opens a fresh session.
func jsServeRPC(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 || args[0].String() == "" {
		return js.Global().Get("Error").New("serveRPC: a ws:// bridge url is required")
	}
	wsURL := args[0].String()
	return promise(func() (interface{}, error) {
		c, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			return nil, err
		}
		// No read limit: RPC frames (summaries, transport lists) can exceed the
		// 32KiB default.
		c.SetReadLimit(-1)
		conn := websocket.NetConn(context.Background(), c, websocket.MessageBinary)
		srv, err := wasmhv.NewRPCServer(visorSelf{}, tpController{})
		if err != nil {
			return nil, err
		}
		go func() {
			err := wasmrpc.ServeTab(conn, func(s io.ReadWriteCloser) { srv.ServeConn(s) })
			vlog("rpc bridge: session ended: " + errStr(err))
		}()
		vlog("rpc bridge: serving visor RPC over " + wsURL)
		return "ok", nil
	})
}

func errStr(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
