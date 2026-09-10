// Package wasmhv pkg/wasmhv/embed.go c3-vis-wasm
package wasmhv

import (
	"bytes"
	_ "embed"
	"encoding/json"

	"github.com/skycoin/skywire/pkg/wasmhv/browseui"
)

// BrowseJS is the desk bundle (bottle's jsfs + vnet + proc, the skywire
// seeding and exec glue, the Go-browser launcher) served as /browse.js beside
// every desk page.
//
// Re-exported from the dependency-free browseui leaf package so pkg/visor can
// serve the SAME bundle in the native hypervisor UI without an import cycle.
var BrowseJS = browseui.BrowseJS

// DeskBootJS is the shared desk boot (skywireDeskBoot) behind the desk-first
// pages — the docs playground and `hv serve`'s /desk. Re-exported from the
// browseui leaf like BrowseJS.
func DeskBootJS() []byte { return browseui.DeskBootJS() }

// VNetSWJS is bottle's vnet service worker, served beside the desk pages as
// /vnet-sw.js (a service worker's scope is capped at its script's directory).
// Re-exported from the browseui leaf like BrowseJS.
func VNetSWJS() []byte { return browseui.VNetSWJS() }

// ExecWorkerJS is the worker bundle served beside the desk pages as
// /skywire-worker.js — the thread every skywire COMMAND runs on, so the visor's
// Go runtime is not the thing scheduling on the page's main thread. Re-exported
// from the browseui leaf like BrowseJS.
func ExecWorkerJS() []byte { return browseui.ExecWorkerJS }

// WalletConfigHTML is the single wallet-config page (served at /wallet/config by
// both the native HV and `hv serve`, embedded via iframe by the ☰ wallet window
// and the Angular wallet tab). Re-exported from the browseui leaf like BrowseJS.
var WalletConfigHTML = browseui.WalletConfigHTML

// AutoUpdateJS is pkg/wasmhv/autoupdate.js — the self-update poller for the
// `hv serve` desk: it compares a /wasm-version fingerprint against the version
// the page booted with and reloads to a newer build (toast + opt-out). Injected
// ONLY by hv serve, so it never runs for a native-hosted hypervisor UI.
//
//go:embed autoupdate.js
var AutoUpdateJS []byte

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

// Real-origin mesh browser, served on the isolated browse origin B
// (<id>.mesh.localhost) and the visor origin V.
//
// The worker, the bridge protocol and the trust boundary come from
// github.com/0magnet/realorigin — realorigin.ServiceWorkerJS() and
// realorigin.ResponderJS(). Only two pieces stay here, and both are ours rather
// than the substrate's:
//
//	BrowseBootstrapHTML — B's navigation shell. The library ships a plain one;
//	                      this keeps skywire's, which shows the live route-setup
//	                      log while a mesh route is being built.
//	BrowseTransportJS   — the transport realorigin calls: dmsg, skynet and
//	                      skysocks-lite through V's own first-party visor.
//
//go:embed browse-bootstrap.html
var BrowseBootstrapHTML []byte

//go:embed browse-transport.js
var BrowseTransportJS []byte

// PWAIcon192 / PWAIcon512 are the maskable install icons referenced by the
// manifest, served at /icon-192.png and /icon-512.png.
//
//go:embed icon-192.png
var PWAIcon192 []byte

//go:embed icon-512.png
var PWAIcon512 []byte

// FaviconICO is the wasm-visor's browser-tab favicon: the Skywire mesh-cloud
// mark tinted violet, so a wasm-visor tab is distinct at a glance from a
// host-native hypervisor tab. Served at /favicon.ico by ServeWasm.
//
//go:embed favicon.ico
var FaviconICO []byte

// WasmExecJS is Go's lib/wasm/wasm_exec.js — the loader every page serves
// beside the one skywire command module (/wasm_exec.js, and the role-pinned
// copies execwasm.LoaderJS derives from it). It MUST match the Go toolchain
// that built the module — refresh it from $(go env GOROOT)/lib/wasm/wasm_exec.js
// when the toolchain moves.
//
//go:embed wasm_exec.js
var WasmExecJS []byte

// ServiceWorkerFor renders sw.js for one serving context: build is the
// fingerprint that names the cache (a new build ships a byte-different worker,
// which is what makes the browser re-install it), precache the shell files
// installed up front — the rest is cached as it is fetched.
func ServiceWorkerFor(build string, precache []string) []byte {
	list, _ := json.Marshal(precache) //nolint:errcheck // a []string always marshals
	out := bytes.ReplaceAll(ServiceWorkerJS, []byte("__BUILD__"), []byte(build))
	return bytes.ReplaceAll(out, []byte("__PRECACHE__"), list)
}
