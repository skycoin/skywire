//go:build !(js && wasm)

// Package commands cmd/skycoin/commands/cipherwasm.go c4-app-wallet
package commands

import (
	skycoinweb "github.com/skycoin/skycoin/cmd/skycoin-web/commands"
	wasmtinygo "github.com/skycoin/skycoin/src/skycoin-lite/wasm-tinygo"
)

// registerCipherWasm supplies the cipher wasm that `skywire skycoin web` serves
// at /assets/scripts/: skycoin's TinyGo build of skycoin-lite, the same one the
// hypervisor serves to the dashboard's wallet (pkg/visor), so skywire carries
// one cipher. It is gzipped as embedded (1.35 MB) and skycoin-web serves it
// that way, with TinyGo's own wasm loader.
func registerCipherWasm() {
	skycoinweb.RegisterCipherWasm(wasmtinygo.WasmFileGz, wasmtinygo.WasmExecJS)
	cipherWasmRegistered = true
}

// cipherWasmRegistered records whether registerCipherWasm supplied a cipher.
// skycoin-web keeps its own copy of this state unexported, so there is no way to
// ask it from here; the alternative to tracking it is that a build silently
// serving no cipher looks exactly like one that works.
var cipherWasmRegistered bool

// cipherWasmAvailable reports whether `skywire skycoin web` can serve a cipher.
func cipherWasmAvailable() bool { return cipherWasmRegistered }
