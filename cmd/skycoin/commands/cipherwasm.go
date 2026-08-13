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
// and a Go wasm needs Go's — so both are taken from one variant, never mixed.
//
// The TinyGo variant is preferred here, which is the opposite of what the visor
// itself defaults to, because this route pays for the whole blob on page load
// and uses only the cipher out of it:
//
//	wasm-visor, std Go     10.0 MB gzip
//	wasm-visor, TinyGo      3.9 MB gzip
//	skycoin-lite (was)      1.7 MB gzip
//
// `skywire skycoin web` runs the wallet against clearnet nodes with the visor
// dormant, so serving the std-Go visor would make that page ~6x heavier than the
// skycoin-lite it replaced for a cipher it does not otherwise use. TinyGo brings
// that back to ~2x. It is still not free — see the note in the PR — but a
// standard-Go skywire embeds BOTH pairs, so this costs nothing to prefer.
//
// Where the visor is actually running, the page loads the blob regardless and
// the cipher genuinely is free; that path takes whatever variant it is serving.
func registerCipherWasm() {
	if !wasmbin.Embedded() {
		// No wasm visor is embedded — a build made without the committed blobs.
		// Leaving the assets unregistered makes the two routes 404, which is
		// what skycoin-web does for any host that serves no cipher.
		return
	}

	// A TinyGo build of skywire embeds only the TinyGo pair; a standard-Go build
	// embeds both. Falling back to the default covers the former.
	v := wasmbin.Default()
	if wasmbin.Has(wasmbin.TinyGo) {
		v = wasmbin.TinyGo
	}

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
