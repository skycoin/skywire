//go:build js && wasm

// Package network pkg/transport/network/webrtc_capability_browser.go c2-net-transport
//
// Runtime capability probe for the browser WebRTC carrier. The SAME js/wasm
// binary can run on a page's main thread (RTCPeerConnection present), in a
// worker whose host page installs the __skywireRTC bridge (a proxy to a
// main-thread peer connection), or inside a plain Web Worker such as the
// desk's exec worker (neither) — so this must be checked
// at runtime, per realm, not by build tag or config.
package network

import "syscall/js"

// WebRTCAvailable reports whether this js realm can construct a peer connection:
// directly (page main thread) or through a host-installed main-thread bridge. Mirrors newPeerConnection's own
// lookup order, so a true here means a dial has a real peer connection to use.
func WebRTCAvailable() bool {
	return js.Global().Get("__skywireRTC").Truthy() || js.Global().Get("RTCPeerConnection").Truthy()
}
