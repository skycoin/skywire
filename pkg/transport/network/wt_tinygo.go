//go:build tinygo && !(js && wasm)

// Package network pkg/transport/network/wt_tinygo.go c2-net-transport
//
// TinyGo (non-browser/embedded) WT-transport stubs. A browser is handled by
// wt_browser.go (js && wasm, either toolchain), which provides its own Start +
// dial + resolve; this file covers embedded TinyGo targets (no HTTP/3 server, no
// dial — wt_tinygo_nows.go stubs the dial). Kept disjoint from wt_browser.go so
// tinygo+js+wasm doesn't get two definitions of Start/dialResolvedWT.
package network

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/skycoin/skywire/pkg/cipher"
)

// dialResolvedWT has no address resolver on a TinyGo target (addrresolver is
// !tinygo), so WT dials come only from the explicit table.
func (c *wtClient) dialResolvedWT(_ context.Context, _ cipher.PubKey) (net.Conn, error) {
	return nil, ErrWTEntryNotFound
}

// errWTServe is returned by Start on TinyGo (no listening socket / no HTTP/3).
// It wraps ErrServeDialOnly so the transport manager logs the expected
// dial-only condition at Debug rather than ERROR/Fatal.
var errWTServe = fmt.Errorf("wt: serving not supported on this build (TinyGo) — a browser/embedded target has no HTTP/3 listener: %w", ErrServeDialOnly)

// errWTDial is returned by the WT dial stub on non-browser TinyGo targets.
var errWTDial = errors.New("wt: WebTransport dial not supported on this TinyGo target")

// Start implements Client: a browser/embedded TinyGo target cannot accept WT.
func (c *wtClient) Start() error {
	return errWTServe
}
