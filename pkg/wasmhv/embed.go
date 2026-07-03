package wasmhv

import (
	_ "embed"

	"github.com/skycoin/skywire/pkg/wasmhv/browseui"
)

// OverrideJS is pkg/wasmhv/override.js — the classic <script> that, in a
// generated standalone file, boots the WASM dmsg client and routes the UI's
// /api over dmsg (or to the in-wasm core in standalone mode). Embedded so
// GenerateStandalone consumers don't need to locate it on disk.
//
//go:embed override.js
var OverrideJS []byte

// BrowseJS is pkg/wasmhv/browse.js — the dmsg virtual-browser engine (the same
// file the wasm-visor dev harness loads). Injected into a generated standalone
// file in VISOR mode so the page gets a browse/host overlay (skynet sites
// rendered + self-hosting over dmsg, via globalThis.skywireVisor). Unused in
// viewer/standalone-hypervisor modes (no skywireVisor).
//
// Re-exported from the dependency-free browseui leaf package so pkg/visor can
// serve the SAME engine in the native hypervisor UI without an import cycle
// (pkg/wasmhv's gob-mirror test imports pkg/visor).
var BrowseJS = browseui.BrowseJS

// AutoUpdateJS is pkg/wasmhv/autoupdate.js — the wasm-visor self-update poller for
// the `hv serve` page: it compares a /wasm-version fingerprint against the version
// the page booted with and reloads to a newer build (toast + opt-out). Injected
// ONLY by hv serve, so it never runs for a native-hosted hypervisor UI.
//
//go:embed autoupdate.js
var AutoUpdateJS []byte

// HvBootJS is pkg/wasmhv/hv-boot.js — the clean boot bootstrap for serving the
// wasm-VISOR hypervisor UI as separate files (the `hv serve` / dev-harness model,
// as opposed to the single-file generator's inlined override.js). It sets
// CFG.visor, loads wasm_exec.js + wasm-visor.wasm, calls skywireVisor.boot(), and
// exposes the boot promise as CFG.ready — which the UI's SkywireHttpBackend awaits
// before its first /api call. Routing is owned by the Angular HttpBackend, so no
// fetch/XHR monkey-patch (unlike override.js).
//
//go:embed hv-boot.js
var HvBootJS []byte

// WorkerJS is pkg/wasmhv/worker.js — the dedicated Web Worker that hosts the Go/wasm
// visor runtime OFF the page's main thread. hv-boot.js spawns it and proxies every
// globalThis.skywireVisor call to it over postMessage, so the visor's blocking work
// (dmsg/WS/WT dials, route setup) can't freeze the UI event loop. Served alongside
// hv-boot.js by `hv serve` (the served model); the single-file generator keeps the
// in-page path since it can't load a separate worker script.
//
//go:embed worker.js
var WorkerJS []byte

// CtlBridgeJS is pkg/wasmhv/ctl-bridge.js — the browser side of the ctlbridge
// control surface (pkg/wasmhv/ctlbridge). It connects the tab to /ctl/events
// over SSE so a shell can drive the in-tab wasm-visor. Injected ONLY when the
// harness control surface is enabled (the dev harness, or `hv serve --harness`),
// never on the public serving path.
//
//go:embed ctl-bridge.js
var CtlBridgeJS []byte

// PWAManifest is pkg/wasmhv/manifest.webmanifest — the Web App Manifest that
// makes the `hv serve` page installable (name, icons, standalone display). Served
// at /manifest.webmanifest and linked from the injected <head>. Only meaningful
// for the served standalone (a service worker needs a real same-origin URL), so
// it is NOT used by the single-file generator.
//
//go:embed manifest.webmanifest
var PWAManifest []byte

// ServiceWorkerJS is pkg/wasmhv/sw.js — the PWA service worker: precaches the app
// shell and serves cache-first for content-hashed bundles / network-first for
// everything else (so autoupdate.js stays correct online, offline still launches).
//
//go:embed sw.js
var ServiceWorkerJS []byte

// PWAIcon192 / PWAIcon512 are the maskable install icons referenced by the
// manifest, served at /icon-192.png and /icon-512.png.
//
//go:embed icon-192.png
var PWAIcon192 []byte

//go:embed icon-512.png
var PWAIcon512 []byte

// WasmExecJS is Go's lib/wasm/wasm_exec.js, vendored here so a generated file is
// self-contained. It MUST match the Go toolchain that built the embedded/passed
// dmsg.wasm — refresh it with the wasm build (the Makefile bundle target copies
// it from $(go env GOROOT)/lib/wasm/wasm_exec.js).
//
//go:embed wasm_exec.js
var WasmExecJS []byte
