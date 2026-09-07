//go:build !js || !wasm

// Package main cmd/wasm-visor/main_host.go c3-vis-wasm
// cmd/wasm-visor is a js/wasm program: its real main() is in main.go behind
// `//go:build js && wasm`. vnetaddr.go, however, is deliberately left untagged
// so `go test ./cmd/wasm-visor` exercises vnetTarget on the host — which makes
// this directory a main package on every OTHER platform too, one with no main
// function. `go build ./cmd/...` therefore fails on the host with
//
//	# github.com/skycoin/skywire/cmd/wasm-visor
//	runtime.main_main·f: function main is undeclared in the main package
//
// (`go test` is unaffected, which is why only the CodeQL workflow — the one
// place that builds ./cmd/... — went red.) This host-side main exists purely to
// keep the package linkable off js/wasm; it does no work and ships nowhere.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "wasm-visor is a WebAssembly program: build it with GOOS=js GOARCH=wasm (see 'make build-wasm').")
	os.Exit(1)
}
