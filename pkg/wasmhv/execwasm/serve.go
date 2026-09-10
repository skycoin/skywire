// Package execwasm pkg/wasmhv/execwasm/serve.go c3-wasm-embed
package execwasm

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
)

// OriginPath is where every page origin that carries the module serves it:
// `hv serve` and the native hypervisor both answer it at the root. The
// surfaces that run the module in a ROLE (the tpviz WebGL view, the wallet
// cipher) fetch it under their own paths; Serve sends them here when this
// server holds no copy.
const OriginPath = "/skywire.wasm"

// ServeGz answers a GET for the module from gz, the gzipped module bytes, with
// stamp as its ETag (a conditional request short-circuits to 304). The bytes go
// out as-is with Content-Encoding: gzip when the client accepts it and are
// inflated on the fly otherwise, so the module is never held inflated in
// memory. len(gz)==0 is a 404. Content-Type and Cache-Control are the caller's
// to set (Serve sets them).
func ServeGz(w http.ResponseWriter, r *http.Request, gz []byte, stamp string) {
	if len(gz) == 0 {
		http.NotFound(w, r)
		return
	}
	if stamp != "" {
		etag := `"` + stamp + `"`
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Vary", "Accept-Encoding")
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(gz) //nolint:errcheck
		return
	}
	zr, err := gzip.NewReader(strings.NewReader(string(gz)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer zr.Close()      //nolint:errcheck
	_, _ = io.Copy(w, zr) //nolint:errcheck,gosec // G110: the module is our own build artifact, not untrusted input
}

// Serve answers a GET for the module from the EMBEDDED copy alone — for a
// server that holds no on-disk path of its own (pkg/tpviz, the wallet routes,
// `skywire skycoin web`), as opposed to pkg/visor's /skywire.wasm which may
// also serve a configured file.
//
// When nothing is embedded the request is redirected to OriginPath. That is
// the one behavior both builds want: the js build never embeds anything (it
// IS the module — see embed_js.go), so a tpviz or wallet route running inside
// a tab's wasm hypervisor can only hand the browser back to the origin that
// delivered the module in the first place, which serves it at OriginPath; a
// native source build without `make embed-exec-wasm` hands it to the same
// place, where the operator's on-disk module answers, or a 404 does. Under a
// /vnet/<port>/ page the browser resolves the redirect against the page's
// real origin, which is exactly the outer server. Never mount Serve AT
// OriginPath: it would redirect to itself.
func Serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	if !Present() {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, OriginPath, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "application/wasm")
	ServeGz(w, r, Gz(), Stamp())
}

// LoaderJS returns Go's wasm_exec.js (execJS, as embedded by pkg/wasmhv) with
// a prelude that fixes the contract of every instance the page creates: `Go`
// is replaced by a subclass whose constructor sets this.argv to
// ["skywire", "desk-host", "--role", role] and gives this.env the same
// HOME/USER/PWD the desk's process layer spawns every command with
// (browseui/skywire-exec.js) — package inits in the root binary resolve the
// current user and directory, and the stock loader hands them nothing. It is
// for a page whose loader script is not ours to change — the vendored
// skycoin-web wallet instantiates assets/scripts/skycoin-lite.wasm with a bare
// `new Go()` — and it is what the tpviz and skysocks status pages get too, so
// one place spells the contract; those pages set go.argv as well, to the same
// value.
func LoaderJS(execJS []byte, role string) []byte {
	prelude := "\n;(function(){var _Go=globalThis.Go;globalThis.Go=class extends _Go{constructor(){super();" +
		"this.argv=['skywire','desk-host','--role','" + role + "'];" +
		"this.env=Object.assign({HOME:'/home/user',USER:'user',PWD:'/home/user'},this.env);}};})();\n"
	out := make([]byte, 0, len(execJS)+len(prelude))
	out = append(out, execJS...)
	return append(out, prelude...)
}
