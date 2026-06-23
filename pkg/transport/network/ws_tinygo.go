//go:build tinygo

// Package network pkg/transport/network/ws_tinygo.go
//
// TinyGo WS-transport carrier. Serving is impossible in a browser (no listening
// socket), so Start fails closed. Dialing IS possible via the browser's native
// WebSocket API, but coder/websocket (used natively) pulls net/http, which does
// not compile here — so the browser dial is wired in a follow-up that adapts the
// syscall/js WebSocket to a net.Conn (the same pattern as the dmsg WS carrier's
// ws_js_tinygo.go). Until then it fails closed so the package builds and a
// browser visor can still use its other transports (dmsg).
package network

import (
	"context"
	"errors"
	"net"
)

// errWSUnsupported is returned by the TinyGo WS carrier stubs.
var errWSUnsupported = errors.New("ws: not supported on this build yet (TinyGo) — browser WebSocket dial is a follow-up; serving needs a listening socket a browser lacks")

func wsDial(_ context.Context, _ string) (net.Conn, error) {
	return nil, errWSUnsupported
}

// Start implements Client: a browser cannot accept WS transports.
func (c *wsClient) Start() error {
	return errWSUnsupported
}
