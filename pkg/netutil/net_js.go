//go:build js

// Package netutil pkg/netutil/net_js.go
//
// js/wasm build of pkg/netutil. The browser has no concept of
// "default network interface" — the WASM runtime can't enumerate
// host NICs at all. The functions exist so transitive importers of
// netutil from packages that DO compile under js (visorconfig and
// friends, via the in-browser install-page WASM at apt-repo) link
// successfully; calling them returns an error explaining the
// situation.
package netutil

import (
	"errors"
)

// DefaultNetworkInterface returns an "unsupported on this
// platform" sentinel error. There's no concept of "the default
// network interface" inside a browser WASM runtime; this stub
// exists so packages that transitively depend on netutil for type
// resolution (struct field references etc.) compile cleanly under
// GOOS=js without needing per-package WASM-aware build tags.
func DefaultNetworkInterface() (string, error) {
	return "", errors.New("netutil: DefaultNetworkInterface unsupported under js/wasm")
}
