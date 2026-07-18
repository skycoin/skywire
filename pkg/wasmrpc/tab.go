// Package wasmrpc pkg/wasmrpc/tab.go c3-vis-wasm
package wasmrpc

import (
	"io"
	"net"

	"github.com/hashicorp/yamux"
)

// ServeTab runs the TAB end of the bridge: it wraps tabConn (the tab's WebSocket
// to the host) in a yamux CLIENT, accepts the streams the host opens (one per
// `skywire cli` connection), and hands each to serve — typically a closure that
// calls rpc.Server.ServeConn(stream). It blocks until the session ends (the WS
// drops or Close is called on the underlying conn), then returns the error.
//
// Pure Go (yamux + net), so it compiles for js/wasm as well as native — the
// wasm-visor calls it with a net.Conn wrapping a browser WebSocket.
func ServeTab(tabConn net.Conn, serve func(stream io.ReadWriteCloser)) error {
	sess, err := yamux.Client(tabConn, yamux.DefaultConfig())
	if err != nil {
		return err
	}
	defer sess.Close() //nolint:errcheck
	for {
		stream, err := sess.Accept()
		if err != nil {
			return err
		}
		go serve(stream)
	}
}
