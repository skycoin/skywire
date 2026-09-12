// The package doc lives in desk.go, which is js/wasm-only. This file carries no
// build tag so that panel-nowasm.js is available to a native binary, which is
// the whole point of having it.
//
// The demo itself is NOT embedded here. It moved to the docs package, beside
// the files it carries, so that importing this package for the desk chrome no
// longer drags 16.4 MB of compiled wasm into a consumer's vendor tree. See
// docs/docs.go for why a directory split is the only thing that works.
package desk

import (
	_ "embed"
)

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
