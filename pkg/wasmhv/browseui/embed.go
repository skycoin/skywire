// Package browseui pkg/wasmhv/browseui/embed.go c3-vis-wasm
// dmsg/clearnet virtual-browser engine plus its window manager — in a
// dependency-free leaf package, so BOTH the wasm-visor (pkg/wasmhv) and the
// native hypervisor UI (pkg/visor) can use it without an import cycle
// (pkg/wasmhv's gob-mirror test imports pkg/visor).
package browseui

import (
	_ "embed"
)

// winBoxJS is WinBox.js (vendored, Apache-2.0, dependency-free, CSS inlined):
// the window manager that provides draggable / resizable / minimizable /
// maximizable window chrome for every mini-desktop window (browse, terminal,
// log, cli). browse.js uses the global `WinBox` it defines.
//
//go:embed winbox.min.js
var winBoxJS []byte

// browseJS is browse.js: the dmsg virtual-browser engine + the mini-desktop
// launcher/windows that build on WinBox.
//
//go:embed browse.js
var browseJS []byte

// BrowseJS is the full mini-desktop bundle (WinBox.js followed by browse.js)
// injected into the wasm-visor page and the native hypervisor dashboard as a
// single script asset. Concatenating here means every consumer
// (pkg/visor's /browse.js handler, pkg/wasmhv's single-file generator, the
// harness) gets WinBox with no extra serve/inline wiring.
var BrowseJS = func() []byte {
	out := make([]byte, 0, len(winBoxJS)+len(browseJS)+4)
	out = append(out, winBoxJS...)
	out = append(out, '\n', ';', '\n')
	out = append(out, browseJS...)
	return out
}()
