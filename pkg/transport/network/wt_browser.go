//go:build tinygo && js && wasm

// Package network pkg/transport/network/wt_browser.go
//
// Browser WT-transport DIAL: a browser visor dials a peer visor's WebTransport
// endpoint via the browser-native WebTransport API, pinning the peer's
// self-signed cert by SHA-256 (serverCertificateHashes). webtransport-go (the
// native carrier, wt_native.go) pulls quic-go, which doesn't compile on the
// TinyGo js target, so we reuse the dmsg carrier's tested syscall/js
// WebTransport→net.Conn adapter (dmsg.DialWebTransportJS), the same one a browser
// visor uses to reach a dmsg server. The returned net.Conn flows through the same
// Noise+yamux initTransport path as every other carrier — so a tab can be the
// dialing edge of a direct WT transport (it still can't accept; see wt_tinygo.go).
package network

import (
	"context"
	"net"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

func wtDial(ctx context.Context, url, certHashHex string) (net.Conn, error) {
	return dmsg.DialWebTransportJS(ctx, url, certHashHex)
}
