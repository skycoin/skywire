//go:build !(js && wasm)

// Package vnet vnet/vnet_native.go
//
// Loopback listen/dial with a browser escape hatch. On native builds the OS
// loopback is this runtime's own loopback and the computer's network at
// once, so both namespaces in vnet.go reach the OS and these are
// net.Listen / net.DialTimeout once the prefix is gone. On js/wasm they
// route loopback addresses through the page's virtual network (vnet.js),
// which is what lets `skywire visor` in one browser terminal and
// `skywire cli` in another reach each other. Callers that bind or dial
// localhost control ports use this package instead of net directly;
// everything else keeps using net.
package vnet

import (
	"net"
	"time"
)

// Listen is net.Listen on native builds.
func Listen(network, address string) (net.Listener, error) {
	addr, _ := Split(address)
	return net.Listen(network, addr)
}

// DialTimeout is net.DialTimeout on native builds.
func DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	addr, _ := Split(address)
	return net.DialTimeout(network, addr, timeout)
}
