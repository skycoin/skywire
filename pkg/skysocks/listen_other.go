//go:build !unix && !js

// Package skysocks pkg/skysocks/listen_other.go c4-app-proxy
package skysocks

import (
	"net"

	"github.com/0magnet/bottle/vnet"
)

// ReuseListen falls back to a plain listen on platforms without the SO_REUSEADDR
// / SO_REUSEPORT socket options wired here (windows, plan9). The
// reconnect-rebind race the unix build avoids is rare enough there that a
// plain listen is the safe default. js/wasm has its own variant (listen_js.go)
// that binds the page's virtual loopback.
func ReuseListen(addr string) (net.Listener, error) {
	// A host: or vnet: address is the OS loopback here. See bottle vnet.
	addr, _ = vnet.Split(addr)
	return net.Listen("tcp", addr)
}
