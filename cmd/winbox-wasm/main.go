//go:build js && wasm

// Package main cmd/winbox-wasm/main.go c3-vis-wasm
// the mini-desktop window manager: a wasm module whose only job is to install
// the WinBox constructor that browse.js builds every window on.
//
// browse.js was written against WinBox.js and calls `new WinBox({...})`
// directly. github.com/0magnet/winbox-go is a Go port of that library, and its
// jsapi subpackage puts the same constructor back on globalThis — so this
// replaces the vendored winbox.min.js without browse.js changing.
//
// Built and committed by `make embed-winbox` into pkg/wasmhv/browseui/, which
// embeds it; winbox-loader.js starts it and publishes __winboxReady. It is
// deliberately its own module rather than part of cmd/wasm-visor: the visor
// runs in a (Shared)Worker, which has no DOM, so it could not install a
// constructor that builds one.
package main

import "github.com/0magnet/winbox-go/jsapi"

func main() {
	jsapi.InstallGlobal()
	select {} // keep the Go runtime alive: every window is driven by callbacks
}
