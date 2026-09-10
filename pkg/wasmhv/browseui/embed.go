// Package browseui pkg/wasmhv/browseui/embed.go c3-vis-wasm
// Assembles the desk bundle from its promoted home — the OS layer
// (github.com/0magnet/bottle: jsfs + vnet + proc) — plus the skywire-specific
// pieces: seed-skywire.js (the package-install filesystem layout),
// skywire-exec.js (per-command execution of the skywire CLI wasm), and
// gobrowser-loader.js (the launcher for the netscrape Go browser, which is
// compiled into the one skywire module as globalThis.skywireBrowser). The
// window manager is not here: the desk chrome is Go (0magnet/desk, linking
// winbox-go directly). A dependency-free-in-skywire leaf package, so BOTH
// pkg/wasmhv and the native hypervisor UI (pkg/visor) can use it without an
// import cycle.
package browseui

import (
	_ "embed"

	"github.com/0magnet/bottle"
)

// seedSkywireJS lays the skywire package tree + /etc/skywire.conf into the
// generic Linux root bottle's jsfs installs. Runs immediately after jsfs.js.
//
//go:embed seed-skywire.js
var seedSkywireJS []byte

// skywireExecJS provides globalThis.skywireExec: one skywire CLI command as a
// PROCESS on bottle's proc layer — the module registered at its package path
// and streamed into the compiler, argv/env/stdio per invocation, an interrupt
// under skywire's own SKYWIRE_EXEC_ID, and the stderr ring the desk reads.
//
//go:embed skywire-exec.js
var skywireExecJS []byte

// execRemoteJS provides globalThis.SkywireExecWorker: the page half of running
// skywire commands in a dedicated Worker instead of on the page main thread. It
// replaces globalThis.skywireExec with a same-contract shim and bridges the
// worker's vnet claims onto the page's port table, so a visor over there is
// indistinguishable from one in here to every panel, the service worker and
// desk-boot's vnet.listening() gates.
//
//go:embed exec-remote.js
var execRemoteJS []byte

// execWorkerJS is the worker half — the tail of ExecWorkerJS() below, not part
// of the page bundle.
//
//go:embed exec-worker.js
var execWorkerJS []byte

// goBrowserLoaderJS defines globalThis.SkywireGoBrowser.open() — the launcher
// for the netscrape Go/wasm browser (github.com/0magnet/netscrape). The browser
// is NOT a separate module: it is compiled into the one skywire module and
// exposed as globalThis.skywireBrowser.open (pkg/wasmhv/deskhost/browser_js.go),
// so this launcher just opens a window and calls that — no second Go runtime.
//
//go:embed gobrowser-loader.js
var goBrowserLoaderJS []byte

// hvwsClientJS is the page-side client for the hypervisor's /ws endpoint
// (pkg/visor/hypervisor_ws.go): globalThis.SkywireHVWS — a capability probe
// plus one multiplexed socket that replays /api calls through the hypervisor's
// own router.
//
//go:embed hvws-client.js
var hvwsClientJS []byte

// hvwsVNetJS is the vnet adapter over that client: globalThis.SkywireHVBridge
// claims the visor's virtual-loopback ports and serves them from the HOST
// visor, so a desk page served by a native hypervisor reaches it through the
// exact vnet calls the in-tab wasm visor answers. Loaded on every desk-ish
// origin; it installs only where a /ws actually answers.
//
//go:embed hvws-vnet.js
var hvwsVNetJS []byte

// deskBootJS is the shared desk boot (skywireDeskBoot(opts)) behind both
// desk-first pages: the docs playground and the converged visor page. Served
// as its own asset (not part of the bundle) because it runs page-level
// decisions the bundle must stay agnostic of.
//
//go:embed desk-boot.js
var deskBootJS []byte

// DeskBootJS returns desk-boot.js.
func DeskBootJS() []byte { return deskBootJS }

// BrowseJS is the full desk bundle — OS layer, host-visor bridge, exec glue,
// Go-browser launcher — served to every desk page (`hv serve`, the native
// hypervisor, the docs playground) as a single script asset. Concatenating
// here means every consumer gets the whole stack with no extra script wiring.
// The window manager is not in it: the desk chrome is Go (0magnet/desk,
// linking winbox-go directly), so no page that boots the desk needs
// globalThis.WinBox.
//
// Order matters: Go instances capture globalThis.fs at START, so jsfs (and
// its skywire seeding), vnet and proc sit at the top, before anything can
// start a wasm module. proc must follow jsfs — it delegates jsfs.stdio.
var BrowseJS = func() []byte {
	parts := [][]byte{
		bottle.JSFS(),
		seedSkywireJS,
		bottle.VNetJS(),
		// The host-visor bridge sits directly on vnet: it is a listener on the
		// same port table, so it belongs beside it and ahead of anything that
		// might dial one of those ports.
		hvwsClientJS,
		hvwsVNetJS,
		bottle.ProcJS(),
		skywireExecJS,
		// Directly after it: the shim that can REPLACE it with a worker-hosted
		// one. Nothing else in the bundle cares which of the two is installed.
		execRemoteJS,
		goBrowserLoaderJS,
	}
	return concat(parts)
}()

// concat joins bundle parts with a statement separator between them — an IIFE
// that ends without a semicolon must not run into the next part's opening
// paren.
func concat(parts [][]byte) []byte {
	n := 0
	for _, p := range parts {
		n += len(p) + 3
	}
	out := make([]byte, 0, n)
	for i, p := range parts {
		if i > 0 {
			out = append(out, '\n', ';', '\n')
		}
		out = append(out, p...)
	}
	return out
}

// ExecWorkerJS is the worker bundle — served BESIDE each desk page as
// skywire-worker.js, and started by exec-remote.js with `new Worker()`.
//
// It is a whole bottle of its own: jsfs (with the same skywire seeding the page
// gets), vnet, proc and skywire-exec, plus the glue that speaks the page's
// protocol. That duplication is the point — a Worker has its own global scope,
// so the visor's filesystem, port table and process table have to exist over
// there, and sharing the page's would take SharedArrayBuffer (bottle's
// fsbridge.js) and therefore cross-origin isolation the desk does not have.
//
// No window-manager, browser or desk parts: this thread runs Go COMMANDS and
// nothing that draws.
var ExecWorkerJS = func() []byte {
	return concat([][]byte{
		bottle.JSFS(),
		seedSkywireJS,
		bottle.VNetJS(),
		bottle.ProcJS(),
		skywireExecJS,
		execWorkerJS,
	})
}()

// VNetSWJS is bottle's vnet service worker — served BESIDE each desk page as
// vnet-sw.js (a service worker's scope is capped at its script's directory,
// so it cannot ride inside the bundle). vnet.enableSW() registers it; from
// then on /vnet/<port>/… are real same-origin URLs into the page's port
// table, and the nested browser loads in-page servers (the hypervisor UI)
// with native resolution instead of the transcoder.
func VNetSWJS() []byte { return bottle.VNetSWJS() }
