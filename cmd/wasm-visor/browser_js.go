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
	ensureFlexColumn(el)
	netscrape.Open(el)
	return nil
}

// ensureFlexColumn makes el a flex column before netscrape mounts into it.
//
// netscrape lays its chrome out as a flex column: the tab strip and address bar
// are fixed-height rows, and the views container that holds the page takes the
// remainder with `position:relative;flex:1;min-height:0` (netscrape browser.go).
// It repairs `position` on the element it is handed — turning a `static` host
// element `relative` so the absolutely-positioned page views have a containing
// block — but it does NOT repair `display`.
//
// So a host that passes a plain block element gets a views container whose
// `flex:1` is inert (no flex parent to distribute along) and whose
// `min-height:0` permits it to collapse. Its only child is an absolutely
// positioned iframe, which contributes no content height, so the container
// resolves to ZERO height. The page inside loads and runs perfectly and is
// simply never visible — which is exactly how it presented: a fully loaded
// hypervisor UI, document title and all, in a frame 1000px wide and 0px tall.
//
// Fixed here rather than in netscrape so no vendored dependency is patched, and
// because the flex context is properly the host's responsibility — netscrape is
// mounted into a WinBox body here, a docked pane elsewhere, and each host knows
// its own layout. Only set when the element is not already a flex container, so
// a host that has arranged this itself is left alone.
func ensureFlexColumn(el js.Value) {
	cs := js.Global().Call("getComputedStyle", el)
	if !cs.Truthy() {
		return
	}
	if d := cs.Get("display").String(); d == "flex" || d == "inline-flex" {
		return
	}
	style := el.Get("style")
	if !style.Truthy() {
		return
	}
	style.Set("display", "flex")
	style.Set("flexDirection", "column")
}
