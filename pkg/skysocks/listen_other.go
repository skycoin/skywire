//go:build !unix

// Package skysocks pkg/skysocks/listen_other.go c4-app-proxy
package skysocks

import "net"

// ReuseListen falls back to a plain listen on platforms without the SO_REUSEADDR
// / SO_REUSEPORT socket options wired here (windows, js/wasm, plan9). The
// reconnect-rebind race the unix build avoids is a non-issue for the wasm
// client (no OS TCP listener) and rare enough elsewhere that a plain listen is
// the safe default.
func ReuseListen(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}
