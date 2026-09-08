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
		"open":   js.FuncOf(jsOpenBrowser),
		"newTab": js.FuncOf(jsBrowserNewTab),
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
	netscrape.Open(netscrapeHost(el))
	return nil
}

// jsBrowserNewTab(url, background?) opens url in a new netscrape tab. A host
// that mounts the browser itself needs this: Open(el) brings the browser up on
// its default page, and without a way to name the first page a host can only
// show that. A no-op before open() — netscrape's own NewTab guards on the
// document it captured there.
//
// Background defaults to false: the caller is normally opening the ONE page the
// window exists for, and that page should have focus.
func jsBrowserNewTab(_ js.Value, args []js.Value) any {
	if len(args) == 0 || args[0].Type() != js.TypeString {
		return nil
	}
	background := len(args) > 1 && args[1].Truthy()
	netscrape.NewTab(args[0].String(), background)
	return nil
}

// netscrapeHost returns an element for netscrape to own, nested one level
// inside el rather than el itself.
//
// netscrape styles its host: it sets display:flex + flexDirection:column so its
// tab strip and address bar sit above a views container that claims the rest
// with `flex:1;min-height:0` (netscrape browser.go). That is correct and
// netscrape does it unprompted — the host does not need to.
//
// The problem is that something else styles the same element AFTERWARDS. The
// desk's tab machinery shows a pane with `view.style.display = "block"`
// (0magnet/desk tabs_js.go, both on add and on every tab SHOW). That overwrites
// netscrape's `display:flex` while leaving `flex-direction:column` behind — the
// contradictory pair is the fingerprint. The views container's `flex:1` then has
// no flex parent to distribute along, `min-height:0` lets it collapse, and its
// only child is an absolutely positioned iframe contributing no content height.
// It resolves to ZERO height, so the page inside loads, runs, and is never
// visible: a fully working hypervisor UI in a frame 1000px wide and 0px tall.
//
// That file's own comment notes "one that styles its host (the browser does)
// needs to find a position it can keep" — position was preserved, display was
// not.
//
// Giving netscrape its own child means the tab machinery keeps setting
// display:block on the outer element, where block is exactly right, and the
// inner element netscrape owns is never touched. No vendored dependency is
// patched and nothing has to win a race over one style property.
func netscrapeHost(el js.Value) js.Value {
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return el
	}
	inner := doc.Call("createElement", "div")
	// position:relative so netscrape leaves position alone (it only supplies one
	// when the host computes to static); 100%/100% so the inner box inherits the
	// outer's geometry whatever the host sized it to.
	inner.Get("style").Set("cssText",
		"position:relative;width:100%;height:100%;display:flex;flex-direction:column;overflow:hidden")
	el.Call("appendChild", inner)
	return inner
}
