//go:build !tinygo

package wasmbin

import (
	"github.com/skycoin/skywire/pkg/wasmhv/wasmbin/wasmgo"
	"github.com/skycoin/skywire/pkg/wasmhv/wasmbin/wasmtinygo"
)

// blob is a gzipped wasm-visor paired with its matching wasm_exec.js loader.
type blob struct {
	gz     []byte
	execJS []byte
}

// A standard-Go build embeds BOTH wasm-visor pairs: the Go-compiled one
// (the default — full crypto/tls + net/http) and the smaller TinyGo-compiled
// one, so a visor serving the PWA can pick either at serve time. A TinyGo build
// embeds only the TinyGo pair (select_tinygo.go), to stay small.
var (
	variants = map[Variant]blob{
		Go:     {gz: wasmgo.WasmGz, execJS: wasmgo.WasmExecJS},
		TinyGo: {gz: wasmtinygo.WasmGz, execJS: wasmtinygo.WasmExecJS},
	}
	defaultVariant = Go
)
