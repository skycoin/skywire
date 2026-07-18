//go:build tinygo

// Package dmsg pkg/dmsg/dmsg/quic_stub.go c1-net-dmsg
//
// TinyGo build stubs for the transports that don't compile under TinyGo:
//
//   - dmsg-over-QUIC (quic-go needs crypto/tls.QUICEncryptionLevel, absent in
//     TinyGo's crypto/tls), and
//   - dmsg-over-WebTransport (pulls quic-go + webtransport-go) — EXCEPT on the
//     browser (js/wasm) target, where dialSessionWT is provided natively via the
//     browser WebTransport API in wt_js_tinygo.go.
//
// A TinyGo dmsg client never dials QUIC — dialSession's switch only reaches it
// if a server advertises a QUIC endpoint, and the stub's error makes it fall
// back to the TCP/WS path. The WT stub here covers the non-browser TinyGo (IoT)
// build via wt_stub_tinygo.go; the browser build dials WT for real. The
// server-side listeners (ServeQUIC / ServeWebTransport) live only in their
// !tinygo files and are simply absent under TinyGo (a TinyGo build is a client,
// not a server). See docs/design/tinygo-dmsg-client.md.
package dmsg

import (
	"context"
	"errors"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

var errNativeTransportUnsupported = errors.New("dmsg: QUIC/WebTransport not supported in this build (TinyGo); falling back to TCP/WS")

func (ce *Client) dialSessionQUIC(_ context.Context, _ *disc.Entry) (ClientSession, error) {
	return ClientSession{}, errNativeTransportUnsupported
}
