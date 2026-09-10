//go:build !(js && wasm)

// Package commands cmd/skycoin/commands/cipherwasm.go c4-app-wallet
package commands

import (
	skycoinweb "github.com/skycoin/skycoin/cmd/skycoin-web/commands"

	"github.com/skycoin/skywire/pkg/wasmhv"
	"github.com/skycoin/skywire/pkg/wasmhv/execwasm"
)

// registerCipherWasm supplies the cipher wasm that `skywire skycoin web` serves
// at /assets/scripts/, out of the one skywire command module the binary
// already embeds (pkg/wasmhv/execwasm) instead of skycoin's separate
// skycoin-lite build.
//
// The wallet needs Cipher and CipherExtras in the page to keep key material in
// the browser. skycoin's own binaries get them from skycoin-lite; the command
// module publishes the identical API from skycoin-lite/wasmcipher.Register()
// when run as `skywire desk-host --role cipher` (pkg/wasmhv/deskhost). So the
// module is a drop-in, with its loader pinned to that role
// (execwasm.LoaderJS) because the wallet's own `new Go()` passes no argv — and
// skywire does not import cmd/skycoin-web/wasmassets: the ~1.8 MB skycoin-lite
// blob stays out of the binary entirely (see the package comment on root.go).
//
// The module is gzipped as embedded and skycoin-web serves it that way. It is
// much larger than the skycoin-lite it replaces (the whole CLI + visor, ~37 MB
// gzipped) — the price of one module for everything; the wallet page pays it
// once per open and the browser caches it by ETag.
func registerCipherWasm() {
	if !execwasm.Present() {
		// No module is embedded — a source build without `make embed-exec-wasm`.
		// Leaving the assets unregistered makes the two routes 404, which is
		// what skycoin-web does for any host that serves no cipher.
		return
	}
	skycoinweb.RegisterCipherWasm(execwasm.Gz(), execwasm.LoaderJS(wasmhv.WasmExecJS, "cipher"))
	cipherWasmRegistered = true
}

// cipherWasmRegistered records whether registerCipherWasm supplied a cipher.
// skycoin-web keeps its own copy of this state unexported, so there is no way to
// ask it from here; the alternative to tracking it is that a build silently
// serving no cipher looks exactly like one that works.
var cipherWasmRegistered bool

// cipherWasmAvailable reports whether `skywire skycoin web` can serve a cipher.
func cipherWasmAvailable() bool { return cipherWasmRegistered }
