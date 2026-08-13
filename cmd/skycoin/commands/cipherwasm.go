// Package commands cmd/skycoin/commands/cipherwasm.go c4-app-wallet
package commands

import (
	skycoinweb "github.com/skycoin/skycoin/cmd/skycoin-web/commands"

	"github.com/skycoin/skywire/pkg/wasmhv/wasmbin"
)

// registerCipherWasm supplies the cipher wasm that `skywire skycoin web` serves
// at /assets/scripts/, using the wasm visor skywire already embeds instead of
// skycoin's separate skycoin-lite build.
//
// The wallet needs Cipher and CipherExtras in the page to keep key material in
// the browser. skycoin's own binaries get them from skycoin-lite; skywire's
// wasm visor publishes the identical API from
// skycoin-lite/wasmcipher.Register(), which cmd/wasm-visor calls before it
// exposes skywireVisor and which does not require boot(). So the visor blob
// serves as a drop-in, and skywire does not import
// cmd/skycoin-web/wasmassets — the ~1.8 MB skycoin-lite blob stays out of the
// binary entirely (see the package comment on root.go).
//
// Both halves come from the SAME variant. A TinyGo wasm needs TinyGo's loader
// and a Go wasm needs Go's, and which variant is the default depends on how
// skywire itself was compiled — a TinyGo build embeds only the TinyGo pair. So
// this asks wasmbin for its default rather than naming one.
func registerCipherWasm() {
	if !wasmbin.Embedded() {
		// No wasm visor is embedded — a build made without the committed blobs.
		// Leaving the assets unregistered makes the two routes 404, which is
		// what skycoin-web does for any host that serves no cipher.
		return
	}

	v := wasmbin.Default()

	gz, execJS := wasmbin.GetVariantGz(v), wasmbin.WasmExecJSVariant(v)
	if len(gz) == 0 || len(execJS) == 0 {
		return
	}

	skycoinweb.RegisterCipherWasm(gz, execJS)
	cipherWasmRegistered = true
}

// cipherWasmRegistered records whether registerCipherWasm supplied a cipher.
// skycoin-web keeps its own copy of this state unexported, so there is no way to
// ask it from here; the alternative to tracking it is that a build silently
// serving no cipher looks exactly like one that works.
var cipherWasmRegistered bool

// cipherWasmAvailable reports whether `skywire skycoin web` can serve a cipher.
func cipherWasmAvailable() bool { return cipherWasmRegistered }
