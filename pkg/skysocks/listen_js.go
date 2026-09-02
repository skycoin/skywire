//go:build js

// Package skysocks pkg/skysocks/listen_js.go c4-app-proxy
package skysocks

import (
	"net"

	"github.com/0magnet/bottle/vnet"
)

// ReuseListen binds the client's local listener through the page's virtual
// loopback (github.com/0magnet/bottle vnet) so the in-process skysocks-client
// app serves 127.0.0.1:1080 to the WHOLE tab: the websh curl in another wasm
// instance and the nested browser's proxy setting dial the same port table.
// The unix SO_REUSEADDR rebind race doesn't exist here — vnet frees the port
// synchronously on close.
func ReuseListen(addr string) (net.Listener, error) {
	return vnet.Listen("tcp", addr)
}
