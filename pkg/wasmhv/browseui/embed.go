// Package browseui pkg/wasmhv/browseui/embed.go c3-vis-wasm
// Assembles the mini-desktop bundle from its promoted homes — the OS layer
// (github.com/0magnet/bottle: jsfs + vnet) and the window manager
// (github.com/0magnet/winbox-go/dist: module + loader glue) — plus the
// skywire-specific pieces: seed-skywire.js (the package-install filesystem
// layout), skywire-exec.js (per-command execution of the skywire CLI wasm),
// and gobrowser-loader.js (the launcher for the netscrape Go browser, which is
// compiled into the wasm-visor binary as globalThis.skywireBrowser).
// A dependency-free-in-skywire leaf package, so BOTH the wasm-visor
// (pkg/wasmhv) and the native hypervisor UI (pkg/visor) can use it without an
// import cycle (pkg/wasmhv's gob-mirror test imports pkg/visor).
package browseui

import (
	"bytes"
	"compress/gzip"
	"io"
	"sync"

	_ "embed"

	"github.com/0magnet/bottle"
	winboxdist "github.com/0magnet/winbox-go/dist"
)

// seedSkywireJS lays the skywire package tree + /etc/skywire.conf into the
// generic Linux root bottle's jsfs installs. Runs immediately after jsfs.js.
//
//go:embed seed-skywire.js
var seedSkywireJS []byte

// skywireExecJS provides globalThis.skywireExec: per-invocation execution of
// the full skywire CLI wasm module (served at /skywire.wasm) against jsfs,
// with swappable stdio so the terminal captures each command's output.
//
//go:embed skywire-exec.js
var skywireExecJS []byte

// goBrowserLoaderJS defines globalThis.SkywireGoBrowser.open() — the launcher
// for the netscrape Go/wasm browser (github.com/0magnet/netscrape). The browser
// is NOT a separate module any more: it is compiled into the wasm-visor binary
// and exposed as globalThis.skywireBrowser.open (cmd/wasm-visor/browser_js.go),
// so this launcher just opens a window and calls that — no second Go runtime.
//
//go:embed gobrowser-loader.js
var goBrowserLoaderJS []byte

// deskBootJS is the shared desk boot (skywireDeskBoot(opts)) behind both
// desk-first pages: the docs playground and the converged visor page. Served
// as its own asset (not part of the bundle) because it runs page-level
// decisions the bundle must stay agnostic of.
//
//go:embed desk-boot.js
var deskBootJS []byte

// DeskBootJS returns desk-boot.js.
func DeskBootJS() []byte { return deskBootJS }

// BrowseJS is the full mini-desktop bundle — OS layer, window manager loader,
// browser engine, skywire glue — injected into the wasm-visor page and the
// native hypervisor dashboard as a single script asset. Concatenating here
// means every consumer (pkg/visor's /browse.js handler, pkg/wasmhv's
// single-file generator, the harness) gets the whole stack with no extra
// script wiring; only the winbox wasm module itself is a separate fetch, from
// WinBoxWasm below.
//
// Order matters: Go instances capture globalThis.fs at START, so jsfs (and
// its skywire seeding) and vnet sit at the top, before anything can start a
// wasm module.
var BrowseJS = func() []byte {
	parts := [][]byte{
		bottle.JSFS(),
		seedSkywireJS,
		bottle.VNetJS(),
		winboxdist.ExecJS(),
		winboxdist.LoaderJS(),
		skywireExecJS,
		goBrowserLoaderJS,
	}
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
}()

// VNetSWJS is bottle's vnet service worker — served BESIDE each desk page as
// vnet-sw.js (a service worker's scope is capped at its script's directory,
// so it cannot ride inside the bundle). vnet.enableSW() registers it; from
// then on /vnet/<port>/… are real same-origin URLs into the page's port
// table, and the nested browser loads in-page servers (the hypervisor UI)
// with native resolution instead of the transcoder.
func VNetSWJS() []byte { return bottle.VNetSWJS() }

// WinBoxWasmGz is the compressed module, for a consumer that ships it inside a
// page (the single-file generator base64s exactly these bytes) rather than
// serving it.
func WinBoxWasmGz() []byte { return winboxdist.WasmGz() }

var (
	winBoxOnce sync.Once
	winBoxWasm []byte
)

// WinBoxWasm is the module as served at /winbox.wasm. Inflated once on first
// use and kept, since every page load asks for the same ~400 kB.
func WinBoxWasm() []byte {
	winBoxOnce.Do(func() {
		zr, err := gzip.NewReader(bytes.NewReader(winboxdist.WasmGz()))
		if err != nil {
			return
		}
		defer zr.Close() //nolint:errcheck
		b, err := io.ReadAll(zr)
		if err != nil {
			return
		}
		winBoxWasm = b
	})
	return winBoxWasm
}
