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

// JSFS returns jsfs.js — the globalThis.fs / globalThis.process filesystem.
func JSFS() []byte { return jsfs }

// VNetJS returns vnet.js — the globalThis.vnet virtual loopback network.
func VNetJS() []byte { return vnetJS }

// ProcJS returns proc.js — the globalThis.proc process layer: spawn another
// wasm module from jsfs as a child that shares the page's fs and vnet, with
// per-process stdio and an exit promise. Load it after jsfs.js and
// wasm_exec.js. See the proc subpackage for the Go adapter.
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
