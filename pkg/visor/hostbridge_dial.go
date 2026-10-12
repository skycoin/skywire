//go:build !(tinygo && js)

// Package visor pkg/visor/hostbridge_dial.go c3-vis-wasm
package visor

import (
	"context"
	"net"

	"github.com/coder/websocket"
)

// dialHostBridgeConn opens the WebSocket to a host bridge. coder/websocket
// uses the browser's WebSocket under Go's js/wasm. ctx bounds only the dial.
func dialHostBridgeConn(ctx context.Context, url string) (net.Conn, error) {
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	// The bridge sends frames of up to 1 MiB (pkg/hostbridge maxFrame).
	ws.SetReadLimit(2 << 20)
	return websocket.NetConn(context.Background(), ws, websocket.MessageBinary), nil
}
