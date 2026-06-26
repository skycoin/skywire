//go:build tinygo && !(js && wasm)

// Package network pkg/transport/network/wt_tinygo.go
//
// TinyGo (non-browser/embedded) WT-transport stubs. A browser is handled by
// wt_browser.go (js && wasm, either toolchain), which provides its own Start +
// dial + resolve; this file covers embedded TinyGo targets (no HTTP/3 server, no
// dial — wt_tinygo_nows.go stubs the dial). Kept disjoint from wt_browser.go so
// tinygo+js+wasm doesn't get two definitions of Start/resolveWTViaAR.
package network

import (
	"context"
	"errors"

	"github.com/skycoin/skywire/pkg/cipher"
)

// resolveWTViaAR has no address resolver on a TinyGo target (addrresolver is
// !tinygo), so WT dials come only from the explicit table. Always ok=false.
func (c *wtClient) resolveWTViaAR(_ context.Context, _ cipher.PubKey) (string, string, bool) {
	return "", "", false
}

// errWTServe is returned by Start on TinyGo (no listening socket / no HTTP/3).
var errWTServe = errors.New("wt: serving not supported on this build (TinyGo) — a browser/embedded target has no HTTP/3 listener")

// errWTDial is returned by the WT dial stub on non-browser TinyGo targets.
var errWTDial = errors.New("wt: WebTransport dial not supported on this TinyGo target")

// Start implements Client: a browser/embedded TinyGo target cannot accept WT.
func (c *wtClient) Start() error {
	return errWTServe
}
