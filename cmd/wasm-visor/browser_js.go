//go:build js && wasm

// Package main cmd/wasm-visor/browser_js.go c3-vis-wasm
// The netscrape Go/wasm browser as a page-realm SURFACE of the wasm-visor
// binary — the same "one binary, several roles" trick shell_js.go documents.
//
// The browser needs a DOM (it builds a tab strip and iframes with syscall/js);
// the visor runs in a SharedWorker, which has none. Rather than ship a SECOND
// wasm module with its own Go runtime (what the earlier /gobrowser.wasm did),
// this binary's DOM-side instance — the one the desk already loads to carry the
// terminal — ALSO exposes the browser. Opening it in the desk costs nothing
// beyond the browser's own code: it shares the loaded instance's runtime.
//
// Exposed as globalThis.skywireBrowser.open(el): the launcher sets
// globalThis.__netscrapeFetch — the visor's dmsg/clearnet transport, bridged
// from the worker through skywireVisor — and calls open(mountElement).
package main

import (
	"syscall/js"

	"github.com/0magnet/netscrape"
)

// installBrowser publishes globalThis.skywireBrowser for the desk launcher:
// open(el) mounts the Go browser into el and runs it in this instance.
func installBrowser() {
	js.Global().Set("skywireBrowser", js.ValueOf(map[string]interface{}{
		"open": js.FuncOf(jsOpenBrowser),
	}))
}

// jsOpenBrowser(el) mounts the netscrape browser into el (an element, or its
// id). It returns nil — the browser lives on through its own event handlers,
// this instance's runtime already being kept alive by the visor/shell.
func jsOpenBrowser(_ js.Value, args []js.Value) any {
	if len(args) == 0 {
		return nil
	}
	el := args[0]
	if el.Type() == js.TypeString {
		el = js.Global().Get("document").Call("getElementById", el.String())
	}
	if !el.Truthy() {
		return nil
	}
	netscrape.Open(el)
	return nil
}
