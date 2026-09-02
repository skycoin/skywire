//go:build js

// Package netutil pkg/netutil/net_js.go c0-com-util
//
// js/wasm build of pkg/netutil. The browser has no concept of
// "default network interface" — the WASM runtime can't enumerate
// host NICs at all. The functions exist so transitive importers of
// netutil from packages that DO compile under js (visorconfig and
// friends, via the in-browser install-page WASM at apt-repo) link
// successfully; calling them returns an error explaining the
// situation.
package netutil

// DefaultNetworkInterface reports the loopback name under js/wasm. The
// browser cannot enumerate host NICs, but callers (the visor Overview,
// survey enrichment) treat this as informational — returning an error made
// the whole RPC call fail, which broke `skywire cli visor info` against a
// visor running in the browser. "lo" is the honest answer for a runtime
// whose only network is a virtual loopback (pkg/vnet) plus dmsg.
func DefaultNetworkInterface() (string, error) {
	return "lo", nil
}
