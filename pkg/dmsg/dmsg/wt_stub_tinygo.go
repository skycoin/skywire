//go:build tinygo && !(js && wasm)

// Package dmsg pkg/dmsg/dmsg/wt_stub_tinygo.go c1-net-dmsg
//
// dmsg-over-WebTransport stub for the NON-browser TinyGo (IoT) target. quic-go +
// webtransport-go don't compile under TinyGo, and an IoT client has no browser
// WebTransport API either, so dialSessionWT errors here and dialSession falls
// back to TCP/WS. The browser TinyGo build (js/wasm) instead gets a real
// dialSessionWT in wt_js_tinygo.go.
package dmsg

import (
	"context"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

func (ce *Client) dialSessionWT(_ context.Context, _ *disc.Entry) (ClientSession, error) {
	return ClientSession{}, errNativeTransportUnsupported
}
