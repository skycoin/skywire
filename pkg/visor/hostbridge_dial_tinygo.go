//go:build tinygo && js

// Package visor pkg/visor/hostbridge_dial_tinygo.go c3-vis-wasm
package visor

import (
	"context"
	"net"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// dialHostBridgeConn opens the WebSocket to a host bridge with the browser's
// WebSocket, as the dmsg carrier does under TinyGo.
func dialHostBridgeConn(ctx context.Context, url string) (net.Conn, error) {
	return dmsg.DialWebSocketJS(ctx, url)
}
