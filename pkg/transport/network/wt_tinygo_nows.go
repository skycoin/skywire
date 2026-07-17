//go:build tinygo && !(js && wasm)

// Package network pkg/transport/network/wt_tinygo_nows.go c2-net-transport
//
// WT dial stub for non-browser TinyGo targets (e.g. wasip1, bare-metal). There
// is no browser WebTransport API here and webtransport-go (quic-go) is excluded,
// so WT dialing is unavailable. The browser dial lives in wt_browser.go.
package network

import (
	"context"
	"net"
)

func wtDial(_ context.Context, _, _ string) (net.Conn, error) {
	return nil, errWTDial
}
