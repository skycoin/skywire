//go:build tinygo

// Package network pkg/transport/network/wt_tinygo.go
//
// TinyGo WT-transport: the SERVE side. A browser (or any TinyGo target) cannot
// run the HTTP/3 WebTransport server a WT transport listener needs, so Start
// fails closed on all TinyGo builds. The DIAL side is target-specific:
// wt_browser.go (tinygo && js && wasm) dials the browser-native WebTransport;
// wt_tinygo_nows.go (other TinyGo targets) stubs it.
package network

import "errors"

// errWTServe is returned by Start on TinyGo (no listening socket / no HTTP/3).
var errWTServe = errors.New("wt: serving not supported on this build (TinyGo) — a browser/embedded target has no HTTP/3 listener")

// errWTDial is returned by the WT dial stub on non-browser TinyGo targets.
var errWTDial = errors.New("wt: WebTransport dial not supported on this TinyGo target")

// Start implements Client: a browser/embedded TinyGo target cannot accept WT.
func (c *wtClient) Start() error {
	return errWTServe
}
