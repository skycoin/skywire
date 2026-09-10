//go:build js && wasm

// Package main cmd/wasm-visor/role_js.go c3-vis-wasm
// How this instance was invoked. The DOM-side roles themselves live in
// pkg/wasmhv/deskhost now (main.go hands them to deskhost.Run); what stays here
// is the dispatch the legacy blob's loaders still key on.
package main

import "syscall/js"

// wasmRole reports how this instance was invoked: the value of
// globalThis.__SKYWIRE_WASM_ROLE__ when set, "visor" (default) otherwise.
func wasmRole() string {
	if r := js.Global().Get("__SKYWIRE_WASM_ROLE__"); r.Type() == js.TypeString {
		return r.String()
	}
	// Fail closed in a service worker. Everywhere else this binary is loaded on
	// the visor origin V, so defaulting to a visor is right. A service worker may
	// be running on an isolated browse origin B, which is untrusted by
	// construction and must never boot a visor; an absent role there means "do
	// nothing", never "do the most privileged thing".
	if inServiceWorker() {
		return "inert"
	}
	return "visor"
}

// inServiceWorker reports whether this instance is running as a ServiceWorker.
// clients+registration together are what distinguish it from a dedicated or
// shared worker, which have neither.
func inServiceWorker() bool {
	g := js.Global()
	return g.Get("clients").Truthy() && g.Get("registration").Truthy()
}

// hasDOM reports whether this instance can touch the document — false in a
// (Shared)Worker.
func hasDOM() bool {
	d := js.Global().Get("document")
	return d.Truthy() && d.Get("createElement").Type() == js.TypeFunction
}
