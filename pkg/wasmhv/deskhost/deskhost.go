//go:build js && wasm

// Package deskhost pkg/wasmhv/deskhost/deskhost.go c3-vis-wasm
//
// The desk host: the DOM-side surfaces of the skywire desk — the websh
// terminal (shell_js.go), the netscrape browser (browser_js.go), the desk
// chrome with its apps (desk_js.go, desk_pair_js.go) and the tpviz WebGL view
// (tpviz_js.go) — as a library the ONE js/wasm build of the root binary runs
// in the page (`skywire desk-host`, cmd/skywire/commands/deskhost_js.go).
//
// # One module
//
// The desk page used to load two Go modules: cmd/wasm-visor as the desk host
// and the root command module for every `skywire …` the terminal runs (and the
// tab's visor). The desk host was only ever the surfaces above with a visor
// core that was never booted, so it is now a subcommand of the command module:
// desk-boot.js spawns `skywire desk-host` through the page's process layer
// (bottle proc.js) with the same URL, the same compile cache and the same
// argv/env contract as every other command instance. cmd/wasm-visor still
// carries a legacy in-page role over Run until it is retired.
//
// # Where the visor is
//
// Nothing here boots a visor. The applets that ask a visor something (about,
// visors, tps, hvapi…) reach the tab's visor — the `skywire autoconfig`
// instance in the exec worker — through its hypervisor UI on the virtual
// loopback (vnet:8001), the same port the dashboard tab renders; on a desk a
// native hypervisor serves, that port is bridged to the host visor. The mesh
// applets (dcurl, dial, aliases) still speak to a globalThis.skywireVisor
// where a page publishes one (the legacy in-page visor); the served desk has
// none, and they say so.
package deskhost

import (
	"fmt"
	"syscall/js"
	"time"
)

// Run installs the surfaces for role and then never returns. Roles:
//
//   - "shell" (and "desk"): the terminal, the browser and the desk chrome —
//     the served desk page.
//   - "browser": netscrape only, with the same-origin DirectLoader, for a page
//     that already has a desk of its own.
//   - "netview": the tpviz WebGL view (globalThis.tpvizGL).
//   - "auto" / "": shell+browser+desk when the realm has a document; nothing
//     in a worker.
//
// Returning would end the program: under wasm_exec the instance's exit
// invalidates every js.FuncOf it published, so the desk would vanish with it.
func Run(role string) {
	switch role {
	case "shell", "desk":
		installShell()
		installBrowser()
		installDesk()
		fmt.Println("deskhost: shell role — call skywireShell.open(el) / skywireBrowser.open(el)")
	case "browser":
		installBrowser()
		installBrowserDirectLoader()
		fmt.Println("deskhost: browser role — call skywireBrowser.open(el)")
	case "netview":
		installNetView()
		fmt.Println("deskhost: netview role — call tpvizGL.init(elId, onEvent)")
	default:
		if hasDOM() {
			installShell()
			installBrowser()
			installDesk()
			fmt.Println("deskhost: ready — skywireShell / skywireBrowser / __skywireDesk installed")
		} else {
			fmt.Println("deskhost: no document in this realm — nothing to install")
		}
	}
	keepAlive()
}

// hasDOM reports whether this instance can touch the document — false in a
// (Shared)Worker.
func hasDOM() bool {
	d := js.Global().Get("document")
	return d.Truthy() && d.Get("createElement").Type() == js.TypeFunction
}

// keepAlive parks main forever with a timer pending. Not a bare select{}: the
// standard-Go wasm runtime hands control back to the browser only when every
// goroutine is blocked, and if nothing is pending either — this role has no
// goroutines of its own until the UI opens a window — it declares "all
// goroutines are asleep - deadlock!" and exits 2 before the shell can ever be
// opened. A pending timer is enough to satisfy the check.
func keepAlive() {
	for {
		time.Sleep(time.Hour)
	}
}

// promise wraps a blocking Go function as a JS Promise, running it on a
// goroutine so the js.FuncOf that returns it never blocks the event loop.
func promise(fn func() (interface{}, error)) interface{} {
	handler := js.FuncOf(func(_ js.Value, pArgs []js.Value) interface{} {
		resolve, reject := pArgs[0], pArgs[1]
		go func() {
			v, err := fn()
			if err != nil {
				reject.Invoke(js.Global().Get("Error").New(err.Error()))
				return
			}
			resolve.Invoke(js.ValueOf(v))
		}()
		return nil
	})
	return js.Global().Get("Promise").New(handler)
}
