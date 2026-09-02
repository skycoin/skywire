//go:build !(js && wasm)

// Package vnet vnet/vnet_native.go
//
// Loopback listen/dial with a browser escape hatch. On native builds these
// are exactly net.Listen / net.DialTimeout. On js/wasm they route LOOPBACK
// addresses through the page's virtual network (vnet.js) when it is
// installed, which is what lets `skywire visor` in one browser terminal and
// `skywire cli` in another reach each other — separate wasm instances have
// no shared network otherwise. Callers that bind or dial localhost control
// ports use this package instead of net directly; everything else keeps
// using net.
package vnet

import (
	"net"
	"time"
)

// Listen is net.Listen on native builds.
func Listen(network, address string) (net.Listener, error) {
	return net.Listen(network, address)
}

// DialTimeout is net.DialTimeout on native builds.
func DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout(network, address, timeout)
}
