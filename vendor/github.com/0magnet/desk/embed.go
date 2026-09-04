// The package doc lives in desk.go, which is js/wasm-only. This file carries no
// build tag so that the embedded assets — and therefore the serve command — are
// available to a native binary, which is the whole point of having them.
package desk

import (
	"embed"
	"io/fs"
)

// Assets is the built demo — the page, the wasm and its loader shim.
//
// Embedding it means the serve command is a single binary with nothing to host
// and nothing to fetch: `desk serve` works on a machine with no network and no
// checkout. The cost is that the binary carries the wasm, and that the build
// has to have been run before this package compiles — which is why docs/ holds
// a committed build rather than being a build artifact.
//
//go:embed docs
var assets embed.FS

// Assets returns the demo's web root, ready to hand to http.FileServerFS.
func Assets() fs.FS {
	sub, err := fs.Sub(assets, "docs")
	if err != nil {
		// docs is embedded above, so this cannot fail at runtime; if it does
		// the package is broken rather than the caller.
		panic("desk: embedded assets missing: " + err.Error())
	}
	return sub
}

// panelNoWasm is the desk chrome as a single plain-JS asset — the taskbar, the
// ☰ launcher menu, and per-window taskbar buttons over winbox — for pages that
// must not (or cannot) load any wasm. The Go panel (panel.go) is the desk's
// real chrome; this asset exists for the host page that is a shell OVER a
// native process and has to render instantly with nothing wasm-shaped booting
// (skywire's native hypervisor desk is the driving case). It publishes
// globalThis.skywireDeskPanel.mount(document, opts).
//
//go:embed panel-nowasm.js
var panelNoWasm []byte

// PanelNoWasmJS returns the no-wasm desk chrome asset (see panelNoWasm).
func PanelNoWasmJS() []byte { return panelNoWasm }
