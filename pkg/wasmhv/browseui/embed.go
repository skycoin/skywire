// Package browseui embeds browse.js — the shared dmsg/clearnet virtual-browser
// engine — in a dependency-free leaf package, so BOTH the wasm-visor (pkg/wasmhv)
// and the native hypervisor UI (pkg/visor) can use it without an import cycle
// (pkg/wasmhv's gob-mirror test imports pkg/visor).
package browseui

import _ "embed"

// BrowseJS is browse.js: the dmsg virtual-browser engine injected into the
// wasm-visor page and the native hypervisor dashboard.
//
//go:embed browse.js
var BrowseJS []byte
