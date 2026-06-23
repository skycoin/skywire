//go:build tinygo && !(js && wasm)

// Package network pkg/transport/network/ws_tinygo_nows.go
//
// WS dial stub for non-browser TinyGo targets (e.g. wasip1, bare-metal). There
// is no browser WebSocket API here and coder/websocket (net/http) is excluded,
// so WS dialing is unavailable. The browser dial lives in ws_browser.go.
package network

import (
	"context"
	"net"
)

func wsDial(_ context.Context, _ string) (net.Conn, error) {
	return nil, errWSDial
}
