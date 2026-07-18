//go:build tinygo

// Package wasmbin pkg/wasmhv/wasmbin/select_tinygo.go c3-vis-wasm
package wasmbin

import "github.com/skycoin/skywire/pkg/wasmhv/wasmbin/wasmtinygo"

// blob is a gzipped wasm-visor paired with its matching wasm_exec.js loader.
type blob struct {
	gz     []byte
	execJS []byte
}

// A TinyGo build embeds ONLY the TinyGo wasm-visor pair, to keep the binary
// small (embedding the Go blob would add ~9.5 MB it has no reason to carry).
// Standard-Go builds embed both (select_go.go).
var (
	variants = map[Variant]blob{
		TinyGo: {gz: wasmtinygo.WasmGz, execJS: wasmtinygo.WasmExecJS},
	}
	defaultVariant = TinyGo
)
