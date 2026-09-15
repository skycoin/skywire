// Package bottle is the OS layer for running Go programs in a browser tab
// the way they run on Linux — the bottle a wasm ship sails in.
//
// Two page-global primitives, one JS file each:
//
//	jsfs.js — an in-memory Linux-layout filesystem installed as
//	          globalThis.fs / globalThis.process. Go's js/wasm runtime routes
//	          the entire os package through this contract, so every wasm
//	          instance on the page shares one POSIX-ish root: one program
//	          writes /etc files, another reads them back.
//	fsbridge.js — the same filesystem, reachable from a Worker: a child blocks
//	          in Atomics.wait while the page answers, which is the synchronous
//	          syscall contract Go's runtime requires. See proc.spawnWorker.
//	vnet.js — a virtual loopback network: a page-global port table with
//	          in-memory byte pipes, so an instance that LISTENS on
//	          127.0.0.1:<port> can be DIALED from another instance — or from
//	          page JS via vnet.httpFetch.
//
// The vnet subpackage is the Go adapter: vnet.Listen / vnet.DialTimeout are
// net.Listen / net.DialTimeout on native builds, and route loopback addresses
// through the page table under js/wasm.
//
// Load order matters: both scripts must run BEFORE a Go wasm instance starts
// (Go captures globalThis.fs at instance start). Application-specific
// filesystem layout is the page's job, via jsfs.mkdirp / jsfs.writeFile, in a
// script loaded after jsfs.js.
package bottle

import (
	_ "embed"
)

//go:embed jsfs.js
var jsfs []byte

//go:embed vnet.js
var vnetJS []byte

//go:embed vnet-sw.js
var vnetSWJS []byte

//go:embed proc.js
var procJS []byte

//go:embed fsbridge.js
var fsbridgeJS []byte

//go:embed coi-sw.js
var coiSWJS []byte

//go:embed coi-register.js
var coiRegisterJS []byte

// JSFS returns jsfs.js — the globalThis.fs / globalThis.process filesystem.
func JSFS() []byte { return jsfs }

// VNetJS returns vnet.js — the globalThis.vnet virtual loopback network.
func VNetJS() []byte { return vnetJS }

// ProcJS returns proc.js — the globalThis.proc process layer: spawn another
// wasm module from jsfs as a child that shares the page's fs and vnet, with
// per-process stdio, a process id, kill(), and an exit promise. A program
// too large to hold as bytes is bound to its path with proc.registerURL and
// streamed straight into the compiler. Load it after jsfs.js; wasm_exec.js
// may be loaded ahead of it or left to the first spawn. See the proc
// subpackage for the Go adapter.
func ProcJS() []byte { return procJS }

// FSBridgeJS returns fsbridge.js — a blocking, synchronous view of the page's
// jsfs for code running in a Worker. proc.spawnWorker uses it to run a child
// off the main thread on the parent's filesystem, so a long build no longer
// freezes the tab. Serve it beside proc.js; the worker loads it by URL.
//
// Requires cross-origin isolation (COOP/COEP) for SharedArrayBuffer. Without
// it spawnWorker refuses and callers fall back to proc.spawn.
func FSBridgeJS() []byte { return fsbridgeJS }

// VNetSWJS returns vnet-sw.js — the service worker that turns virtual
// loopback ports into real same-origin URLs (/vnet/<port>/…), so iframes can
// load in-page servers with native resolution. Serve it at the page's
// directory as vnet-sw.js and call vnet.enableSW() from the page.
func VNetSWJS() []byte { return vnetSWJS }

// COISWJS returns coi-sw.js — the service worker that makes a page
// cross-origin isolated on a host that cannot set headers. It re-serves every
// response with COOP, COEP and CORP attached, which is what SharedArrayBuffer
// requires, and therefore what FSBridgeJS and proc.spawnWorker require.
//
// Serve it at the page's own directory (the scope must cover the page) and
// load COIRegisterJS from the page. It does not conflict with vnet-sw.js:
// that registers at the narrower /vnet/ scope and keeps serving those URLs,
// which are same-origin, and COEP require-corp only demands CORP of
// CROSS-origin subresources.
func COISWJS() []byte { return coiSWJS }

// COIRegisterJS returns coi-register.js — the page half of COISWJS. It
// registers the worker and reloads ONCE after it takes control, because the
// first navigation came from the server without the headers. Load it before
// anything that wants SharedArrayBuffer, and keep tolerating
// crossOriginIsolated being false on first paint.
func COIRegisterJS() []byte { return coiRegisterJS }
