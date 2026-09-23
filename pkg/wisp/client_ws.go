//go:build !js

// Package wisp pkg/wisp/client_ws.go c4-app-proxy
//
// Dialing a Wisp endpoint by URL, off the browser.
//
// HTTPClient and HTTPHeader are honored here and cannot be on js/wasm, which
// is why this has a js counterpart rather than a build-tagged struct field:
// the option belongs to the configuration everywhere, and only its
// availability differs.
package wisp

import (
	"context"
	"fmt"

	"github.com/coder/websocket"
)

// dialWebsocket opens the endpoint and wraps it as a transport.
func dialWebsocket(ctx context.Context, cfg ClientConfig) (Frames, error) {
	conn, _, err := websocket.Dial(ctx, cfg.URL, &websocket.DialOptions{
		HTTPClient:   cfg.HTTPClient,
		HTTPHeader:   cfg.Header,
		Subprotocols: []string{Subprotocol},
	})
	if err != nil {
		return nil, fmt.Errorf("wisp: dial %s: %w", cfg.URL, err)
	}
	conn.SetReadLimit(cfg.ReadLimit)
	return NewWebsocketFrames(conn), nil
}
