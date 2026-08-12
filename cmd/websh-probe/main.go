//go:build js && wasm

// Package main cmd/websh-probe/main.go c3-vis-wasm
// Command websh-probe compile-checks the browser terminal stack vendored in
// third_party/0magnet: the xterm.js port (terminal emulator) and websh (a
// bash-like shell over a virtual filesystem). It is a BUILD-ONLY probe — not a
// runnable terminal — and exists so both wasm lanes keep that code honest:
// `make build-wasm` (standard Go) and `make build-wasm-tinygo` (TinyGo) both
// build it.
//
// The visor-side integration that mounts these in the wasm hypervisor UI lives
// elsewhere; this probe only asserts the libraries stay portable.
package main

import (
	"github.com/skycoin/skywire/third_party/0magnet/websh/shell"
	xterm "github.com/skycoin/skywire/third_party/0magnet/xterm-go"
)

func main() {
	// Reference the two entry points so the linker keeps them: a terminal
	// bound to a DOM element, and a shell over an in-memory filesystem.
	_ = xterm.New(nil)
	if _, err := shell.New(nil, nil, nil, nil); err != nil {
		panic(err)
	}
	select {}
}
