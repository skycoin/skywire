// Package netscrape is the skywire virtual-browser engine: browse.js, a
// page-side browser that renders sites fetched over channels the host
// browser cannot speak — dmsg/skynet by public key, clearnet through a
// skysocks exit, a page-internal virtual loopback (github.com/0magnet/bottle
// vnet), or a real isolated origin (github.com/0magnet/realorigin) — plus
// the mini-desktop (panel, windows, terminals) it lives in, built on the
// WinBox constructor from github.com/0magnet/winbox-go.
//
// The engine is dependency-injected: a hosting page supplies
//
//	fetchDmsg(pkHost, method, path, body)      → {status, body, headers}
//	fetchClearnet(exit, method, url, body, …)  → same shape
//
// (or lets it default to the globalThis.skywireVisor implementations), and
// browse.js does the rest: transcoding into sandboxed iframes with inlined
// subresources, address-bar channel dispatch, history, favicons, a clearnet
// upstream-proxy policy, and window management.
package netscrape

import (
	_ "embed"
)

//go:embed browse.js
var browseJS []byte

// BrowseJS returns browse.js, the full engine as one script asset.
func BrowseJS() []byte { return browseJS }
