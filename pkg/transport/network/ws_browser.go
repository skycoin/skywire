//go:build tinygo && js && wasm

// Package network pkg/transport/network/ws_browser.go
//
// Browser WS-transport DIAL: a browser visor dials a peer visor's WebSocket
// transport endpoint via the browser-native WebSocket API. coder/websocket
// (the native carrier, ws_native.go) pulls net/http, which is broken on the
// TinyGo js target, so we reuse the dmsg carrier's tested syscall/js WebSocket→
// net.Conn adapter (dmsg.DialWebSocketJS). The returned net.Conn flows through
// the same Noise+yamux initTransport path as every other carrier — so a tab can
// be the dialing edge of a direct WS transport (it still can't accept; see
// ws_tinygo.go).
package network

import (
	"context"
	"net"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

func wsDial(ctx context.Context, url string) (net.Conn, error) {
	return dmsg.DialWebSocketJS(ctx, url)
}
