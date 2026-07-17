//go:build tpvizwasm

package tpviz

import "embed"

// Built with -tags tpvizwasm: embed the experimental WebGL/WASM tpviz view.
// This adds ~11 MB (dist/main.wasm) to the binary, so it is opt-in — the view
// still needs work and the legacy JS view is the default (see tpviz.go).

//go:embed dist/*
var wasmDistFS embed.FS

const wasmViewBuilt = true
