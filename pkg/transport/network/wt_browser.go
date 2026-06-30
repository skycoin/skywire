//go:build js && wasm

// Package network pkg/transport/network/wt_browser.go
//
// Browser WT-transport DIAL: a browser visor dials a peer visor's WebTransport
// endpoint via the browser-native WebTransport API, pinning the peer's
// self-signed cert by SHA-256 (serverCertificateHashes). webtransport-go (the
// native carrier, wt_native.go) pulls quic-go, which can't open UDP in a browser
// on EITHER wasm toolchain (TinyGo or std-Go js/wasm), so any js/wasm build
// reuses the dmsg carrier's tested syscall/js WebTransport→net.Conn adapter
// (dmsg.DialWebTransportJS), the same one a browser visor uses to reach a dmsg
// server. The returned net.Conn flows through the same Noise+yamux initTransport
// path as every other carrier — so a tab can be the dialing edge of a direct WT
// transport. A browser can't serve (no HTTP/3 listener), so Start fails closed
// and there is no address resolver to register/resolve against (table only).
package network

import (
	"context"
	"errors"
	"net"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

func wtDial(ctx context.Context, url, certHashHex string) (net.Conn, error) {
	return dmsg.DialWebTransportJS(ctx, url, certHashHex)
}

// Start implements Client: a browser tab cannot run the HTTP/3 WebTransport
// server a WT listener needs, so it fails closed (dial-only, like ws_browser).
func (c *wtClient) Start() error {
	return errors.New("wt: serving not supported in a browser (no HTTP/3 listener) — dial only")
}

// dialResolvedWT is a no-op in the browser: there is no address-resolver client
// (addrresolver is !tinygo and pulls quic-go); WT dials come from the table the
// autoconnect populates with the AR-resolved endpoint + cert hash.
func (c *wtClient) dialResolvedWT(_ context.Context, _ cipher.PubKey) (net.Conn, error) {
	return nil, ErrWTEntryNotFound
}
