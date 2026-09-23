//go:build js

// Package wisp pkg/wisp/client_ws_js.go c4-app-proxy
//
// Dialing a Wisp endpoint by URL from inside a browser.
//
// coder/websocket's js/wasm build wraps the page's own WebSocket object, whose
// DialOptions carry a subprotocol list and nothing else. That is not a gap in
// the library: the WebSocket API gives a page no way to set request headers
// (the subprotocol is the only one it can influence) and no way to supply a
// transport of its own.
//
// So the two options that cannot be honored are refused rather than ignored.
// Silently dropping a proxy the caller asked for would send the traffic
// straight out of the page instead — the precise thing the caller was trying
// to avoid. The refusal names DialConn, which is how that is actually done in
// a tab: dial the conn yourself, over whatever you like, and run the session
// on it.
package wisp

import (
	"context"
	"errors"
	"fmt"

	"github.com/coder/websocket"
)

// dialWebsocket opens the endpoint and wraps it as a transport.
func dialWebsocket(ctx context.Context, cfg ClientConfig) (Frames, error) {
	if cfg.HTTPClient != nil {
		return nil, errors.New("wisp: ClientConfig.HTTPClient cannot be honored in a browser — " +
			"a page's WebSocket has no transport to replace; dial the conn yourself and use DialConn")
	}
	if len(cfg.Header) > 0 {
		return nil, errors.New("wisp: ClientConfig.Header cannot be honored in a browser — " +
			"a page cannot set WebSocket request headers; dial the conn yourself and use DialConn")
	}

	conn, _, err := websocket.Dial(ctx, cfg.URL, &websocket.DialOptions{
		Subprotocols: []string{Subprotocol},
	})
	if err != nil {
		return nil, fmt.Errorf("wisp: dial %s: %w", cfg.URL, err)
	}
	conn.SetReadLimit(cfg.ReadLimit)
	return NewWebsocketFrames(conn), nil
}
