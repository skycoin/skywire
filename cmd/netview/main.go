//go:build js && wasm

// Command netview is the WebGL graph view (pkg/tpviz/wasmgl) as a module of
// its own, so a page that only draws a graph does not load the skywire module.
// `make netview-wasm` builds it into pkg/tpviz/netview.
package main

import (
	"time"

	"github.com/skycoin/skywire/pkg/tpviz/wasmgl"
)

func main() {
	wasmgl.Register()
	// A bare select{} lets the js/wasm runtime report a deadlock and exit
	// while no goroutine has work, which would drop tpvizGL.
	for {
		time.Sleep(time.Hour)
	}
}
