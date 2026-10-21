//go:build !(js && wasm)

package commands

import (
	skycoinweb "github.com/skycoin/skycoin/cmd/skycoin-web/commands"
	wasmtinygo "github.com/skycoin/skycoin/src/skycoin-lite/wasm-tinygo"
)

// The wallet page cannot sign without its cipher wasm. The mobile core does not
// link cmd/skycoin, which registers it on desktop, so the app registers it too.
func init() {
	skycoinweb.RegisterCipherWasm(wasmtinygo.WasmFileGz, wasmtinygo.WasmExecJS)
}
