// Package term serves websh as a terminal page: cmd/websh-term, built with
// TinyGo and embedded here, so a Go server can offer a shell in the browser
// with one handler.
//
// The page is served at any path, and its two assets beside it as ?asset=,
// so it works wherever it is mounted, under a prefix as well. The command it
// starts with is the server's to choose, typically pty with the address of a
// WebSocket that speaks shell/pty's protocol.
//
// Rebuild the module with ./build.sh term after changing cmd/websh-term or
// anything it uses.
package term

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"html/template"
	"net/http"
	"strings"
	"time"
)

//go:embed term.wasm.gz wasm_exec.js
var assets embed.FS

// Page is what one page shows.
type Page struct {
	// Title is the page's title. Empty means "websh".
	Title string
	// Host is the name in the shell's prompt. Empty means "websh".
	Host string
	// Run is the command the shell starts with, run as though typed. Empty
	// leaves it at its prompt. It is written into the page, so it must come
	// from the server, never from the request's address unchecked.
	Run string
}

// Handler serves the page that page describes for each request, and its
// assets.
func Handler(page func(r *http.Request) Page) http.Handler {
	wasm, _ := assets.ReadFile("term.wasm.gz")   //nolint:errcheck // embedded, always present
	execJS, _ := assets.ReadFile("wasm_exec.js") //nolint:errcheck // embedded, always present
	tag := func(b []byte) string {
		sum := sha256.Sum256(b)
		return `"` + hex.EncodeToString(sum[:8]) + `"`
	}
	wasmTag, execTag := tag(wasm), tag(execJS)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("asset") {
		case "wasm_exec.js":
			serveAsset(w, r, execJS, execTag, "text/javascript; charset=utf-8", false)
		case "term.wasm":
			serveAsset(w, r, wasm, wasmTag, "application/wasm", true)
		case "":
			p := page(r)
			if p.Title == "" {
				p.Title = "websh"
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			pageTmpl.Execute(w, p) //nolint:errcheck,gosec // a client gone mid-page has nothing to be told
		default:
			http.NotFound(w, r)
		}
	})
}

// serveAsset sends b, gzipped already when gz, keyed by tag so a browser
// fetches it once per build.
func serveAsset(w http.ResponseWriter, r *http.Request, b []byte, tag, ctype string, gz bool) {
	w.Header().Set("ETag", tag)
	w.Header().Set("Cache-Control", "no-cache")
	if r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", ctype)
	if gz {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			http.Error(w, "the terminal needs a browser that accepts gzip", http.StatusNotAcceptable)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(b))
}

var pageTmpl = template.Must(template.New("term").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="websh-host" content="{{.Host}}">
<meta name="websh-run" content="{{.Run}}">
<title>{{.Title}}</title>
<style>
html, body { margin: 0; height: 100%; background: #000; }
#terminal { position: fixed; inset: 0; }
</style>
</head>
<body>
<div id="terminal"></div>
<script src="?asset=wasm_exec.js"></script>
<script>
const go = new Go();
WebAssembly.instantiateStreaming(fetch("?asset=term.wasm"), go.importObject)
  .then(r => go.run(r.instance))
  .catch(e => { document.getElementById("terminal").textContent = "websh failed to load: " + e; });
</script>
</body>
</html>
`))
