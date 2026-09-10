// Package visor pkg/visor/wallet_cipher.go c3-vis-core
package visor

import (
	"net/http"

	"github.com/skycoin/skywire/pkg/wasmhv"
	"github.com/skycoin/skywire/pkg/wasmhv/execwasm"
)

// serveWalletCipherAsset answers the two assets the vendored skycoin-web
// wallet loads for its cipher, and reports whether rest (the path under the
// /wallet/ mount) was one of them. Shared by `hv serve`'s /wallet/ and the
// native hypervisor's.
//
// The wallet keeps key material in the browser through its window.SkycoinCipher
// globals (skycoin-lite/wasmcipher.Register). Its bundle UNCONDITIONALLY does
// `new Go()` + instantiateStreaming(fetch("assets/scripts/skycoin-lite.wasm"))
// in every window it runs in — it never looks for globals published earlier —
// so the two URLs must answer, and the bundle is not ours to change. They are
// answered out of the ONE skywire command module (pkg/wasmhv/execwasm): the
// wasm is the module itself, and the loader is Go's wasm_exec.js with a
// prelude that makes every `new Go()` run `skywire desk-host --role cipher`
// (pkg/wasmhv/deskhost), which publishes the globals and parks. No separate
// skycoin-lite.wasm, no wasm-visor blob. Where nothing is embedded — a source
// build, or this handler running inside a tab's wasm hypervisor — execwasm
// redirects the wasm fetch to the page origin's /skywire.wasm.
func serveWalletCipherAsset(w http.ResponseWriter, r *http.Request, rest string) bool {
	switch rest {
	case "assets/scripts/skycoin-lite.wasm":
		execwasm.Serve(w, r)
		return true
	case "assets/scripts/wasm_exec.js":
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(walletLoaderJS()) //nolint:errcheck
		return true
	}
	return false
}

// walletLoaderJS is the wallet's wasm_exec.js: Go's loader pinned to the
// module's cipher role.
func walletLoaderJS() []byte { return execwasm.LoaderJS(wasmhv.WasmExecJS, "cipher") }
