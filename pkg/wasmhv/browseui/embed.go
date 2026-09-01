// Package browseui pkg/wasmhv/browseui/embed.go c3-vis-wasm
// dmsg/clearnet virtual-browser engine plus its window manager — in a
// dependency-free leaf package, so BOTH the wasm-visor (pkg/wasmhv) and the
// native hypervisor UI (pkg/visor) can use it without an import cycle
// (pkg/wasmhv's gob-mirror test imports pkg/visor).
package browseui

import (
	"bytes"
	"compress/gzip"
	"io"
	"sync"

	_ "embed"
)

// winBoxWasmGz is the window manager: cmd/winbox-wasm, a Go port of WinBox.js
// (github.com/0magnet/winbox-go, Apache-2.0) compiled to wasm and gzipped. It
// provides the draggable / resizable / minimizable / maximizable chrome for
// every mini-desktop window (browse, terminal, log, cli) by installing the
// global `WinBox` constructor browse.js calls.
//
// Committed rather than built here: `go build` produces one host binary, and
// this is a wasm artifact from a second toolchain. Update it INTENTIONALLY with
// `make embed-winbox`, which rebuilds, gzips (-n, so an unchanged module yields
// no diff) and refreshes winbox-exec.js alongside it.
//
//go:embed winbox.wasm.gz
var winBoxWasmGz []byte

// winBoxExecJS is TinyGo's wasm_exec.js, wrapped by `make embed-winbox` so its
// loader class lands on __winboxGo instead of globalThis.Go — see the comment
// at the top of the generated file.
//
//go:embed winbox-exec.js
var winBoxExecJS []byte

// winBoxLoaderJS starts the module and publishes __winboxReady.
//
//go:embed winbox-loader.js
var winBoxLoaderJS []byte

// browseJS is browse.js: the dmsg virtual-browser engine + the mini-desktop
// launcher/windows that build on the WinBox global.
//
//go:embed browse.js
var browseJS []byte

// jsfsJS installs the in-memory Linux-layout filesystem as globalThis.fs /
// globalThis.process (see jsfs.js). It sits at the TOP of the bundle: Go's
// js/wasm runtime captures globalThis.fs when an instance STARTS, so
// installing here — before browse.js can instantiate the DOM-side shell and
// before any skywire command executes — puts every page-realm Go instance on
// the one shared filesystem. Idempotent; the SharedWorker visor is a separate
// realm and unaffected.
//
//go:embed jsfs.js
var jsfsJS []byte

// skywireExecJS provides globalThis.skywireExec: per-invocation execution of
// the full skywire CLI wasm module (served at /skywire.wasm) against jsfs,
// with swappable stdio so the terminal captures each command's output.
//
//go:embed skywire-exec.js
var skywireExecJS []byte

// BrowseJS is the full mini-desktop bundle — the wasm loader followed by
// browse.js — injected into the wasm-visor page and the native hypervisor
// dashboard as a single script asset. Concatenating here means every consumer
// (pkg/visor's /browse.js handler, pkg/wasmhv's single-file generator, the
// harness) gets the window manager with no extra script wiring; only the wasm
// module itself is a separate fetch, from WinBoxWasm below.
var BrowseJS = func() []byte {
	out := make([]byte, 0, len(jsfsJS)+len(winBoxExecJS)+len(winBoxLoaderJS)+len(browseJS)+len(skywireExecJS)+16)
	out = append(out, jsfsJS...)
	out = append(out, '\n', ';', '\n')
	out = append(out, winBoxExecJS...)
	out = append(out, '\n', ';', '\n')
	out = append(out, winBoxLoaderJS...)
	out = append(out, '\n', ';', '\n')
	out = append(out, browseJS...)
	out = append(out, '\n', ';', '\n')
	out = append(out, skywireExecJS...)
	return out
}()

// WinBoxWasmGz is the compressed module, for a consumer that ships it inside a
// page (the single-file generator base64s exactly these bytes) rather than
// serving it.
func WinBoxWasmGz() []byte { return winBoxWasmGz }

var (
	winBoxOnce sync.Once
	winBoxWasm []byte
)

// WinBoxWasm is the module as served at /winbox.wasm. Inflated once on first
// use and kept, since every page load asks for the same ~400 kB.
func WinBoxWasm() []byte {
	winBoxOnce.Do(func() {
		zr, err := gzip.NewReader(bytes.NewReader(winBoxWasmGz))
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
