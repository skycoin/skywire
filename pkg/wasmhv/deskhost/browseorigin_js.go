//go:build js && wasm

// Package deskhost pkg/wasmhv/deskhost/browseorigin_js.go c3-vis-wasm
// Installs netscrape's OriginLoader: the half that turns a mesh address or a
// clearnet site into a
// REAL isolated browse origin instead of a sandboxed srcdoc.
//
// This is what was missing. realorigin shipped the substrate, wasm-serve
// deployed B and answered its bootstrap, and browse-responder.js published
// realOrigin.register on V — but after the Go rewrite of the browser nothing
// ever CALLED register, so no frame was ever pointed at a browse origin and
// every mesh page fell through to the transcoder. The retired browse.js engine
// did this; netscrape did not inherit it.
//
// The rules are in browseorigin.go, untagged and tested. Only the DOM and
// promise plumbing lives here.
package deskhost

import (
	"syscall/js"

	"github.com/0magnet/netscrape"
)

// installOriginLoader wires netscrape.OriginLoader to realorigin. Both roles
// that mount the browser call it; it is idempotent.
//
// It claims nothing unless the page carries BOTH halves: the
// __SKYWIRE_BROWSE_ORIGIN__ config (which says where browse origins live) and
// a realOrigin with a register (the responder). A desk served without them —
// any build older than this one — keeps the transcoder, which is the whole
// fallback story: nothing here can make browsing worse than it already was.
func installOriginLoader() {
	netscrape.OriginLoader = func(u string) (js.Value, bool) {
		cfg := js.Global().Get("__SKYWIRE_BROWSE_ORIGIN__")
		if !cfg.Truthy() {
			return js.Undefined(), false
		}
		ro := js.Global().Get("realOrigin")
		if !ro.Truthy() || ro.Get("register").Type() != js.TypeFunction {
			return js.Undefined(), false
		}
		parsed, ok := parseURL(u)
		if !ok {
			return js.Undefined(), false
		}
		// The descriptor is what the responder binds to the frame's private
		// port, and what browse-transport.js fetches against. The frame never
		// sees it and can never name another site's. A mesh address is keyed by
		// its network and resolver host; a clearnet site by its origin, which
		// browse-transport.js rebases the frame's requests onto.
		var descriptor map[string]any
		var canonical string
		hostname := parsed.Get("hostname").String()
		if network, host, mesh := meshOriginFor(hostname); mesh {
			descriptor = map[string]any{"net": network, "host": host}
			canonical = canonicalMeshTarget(network, host)
		} else if base, clearnet := clearnetOriginFor(parsed.Get("protocol").String(), hostname, parsed.Get("origin").String()); clearnet {
			descriptor = map[string]any{"net": "skysocks", "base": base}
			canonical = canonicalClearnetTarget(base)
		} else {
			return js.Undefined(), false
		}
		suffix := stringOr(cfg.Get("suffix"), ".mesh.localhost")
		scheme := stringOr(cfg.Get("scheme"), "https")
		port := stringOr(cfg.Get("port"), "")
		path := parsed.Get("pathname").String() + parsed.Get("search").String()
		if path == "" {
			path = "/"
		}

		promise := ro.Call("register", canonical, descriptor)
		var build js.Func
		build = js.FuncOf(func(_ js.Value, a []js.Value) any {
			defer build.Release()
			if len(a) == 0 || a[0].Type() != js.TypeString {
				return nil // netscrape reads a non-string as "no claim" and transcodes
			}
			origin := scheme + "://" + a[0].String() + suffix
			if port != "" {
				origin += ":" + port
			}
			return origin + path
		})
		return promise.Call("then", build), true
	}
}

// parseURL runs the platform's URL parser, which is the one the rest of this
// path agrees with. It reports false rather than throwing on a bad URL.
func parseURL(u string) (js.Value, bool) {
	defer func() { _ = recover() }() //nolint:errcheck // a throwing URL constructor reports false
	ctor := js.Global().Get("URL")
	if ctor.Type() != js.TypeFunction {
		return js.Undefined(), false
	}
	v := ctor.New(u)
	if !v.Truthy() {
		return js.Undefined(), false
	}
	return v, true
}

func stringOr(v js.Value, fallback string) string {
	if v.Type() == js.TypeString && v.String() != "" {
		return v.String()
	}
	return fallback
}
