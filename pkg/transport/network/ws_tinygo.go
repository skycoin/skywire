//go:build tinygo

// Package network pkg/transport/network/ws_tinygo.go
//
// TinyGo WS-transport: the SERVE side. A browser (or any TinyGo target) cannot
// run the WebSocket HTTP server a WS transport listener needs, so Start fails
// closed on all TinyGo builds. The DIAL side is target-specific: ws_browser.go
// (tinygo && js && wasm) dials the browser-native WebSocket; ws_tinygo_nows.go
// (other TinyGo targets) stubs it.
package network

import (
	"context"
	"errors"

	"github.com/skycoin/skywire/pkg/cipher"
)

// resolveWSURLViaAR has no address resolver on a TinyGo target (addrresolver is
// !tinygo), so WS dials come only from the explicit table. Always ok=false.
func (c *wsClient) resolveWSURLViaAR(_ context.Context, _ cipher.PubKey) (string, bool) {
	return "", false
}

// errWSServe is returned by Start on TinyGo (no listening socket).
var errWSServe = errors.New("ws: serving not supported on this build (TinyGo) — a browser/embedded target has no listening socket")

// errWSDial is returned by the WS dial stub on non-browser TinyGo targets.
var errWSDial = errors.New("ws: WebSocket dial not supported on this TinyGo target")

// Start implements Client: a browser/embedded TinyGo target cannot accept WS.
func (c *wsClient) Start() error {
	return errWSServe
}
