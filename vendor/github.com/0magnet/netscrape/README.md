# netscrape

A web browser written in Go/wasm.

**[Live demo](https://netscrape.magnetosphere.net/)** — the browser, browsing a small site served beside it: stylesheet inlined, image relayed back as a `data:` URI, links and a GET form posted to the Go chrome.

![netscrape in the browser](docs/netscrape-demo.png "the Go chrome — tab strip, address bar, history — rendering a sample page with its stylesheet and image transcoded in")

A static page can only fetch its own origin, so the demo reaches the sample
site and says so for anything else. That limit is the transport's, not the
browser's: the same seam dials the dmsg mesh in skywire's wasm visor, and in
[shipyard](https://shipyard.magnetosphere.net/) it reaches a Go server running
in the same tab over a virtual loopback.

The chrome — a tab strip, an address bar, back/forward/reload, history — is DOM
built from Go with `syscall/js`. Each tab is a sandboxed `<iframe>`. A page is
fetched over a **host-supplied transport**, transcoded (rendered into a
sandboxed `srcdoc` with its stylesheets inlined as `<style>` and its images as
`data:` URIs, via a parent/sandbox relay), and its links and form submits are
relayed back to the chrome. The browser is Go; only the rendering (the iframe)
and the network (the transport) are delegated.

That transport is the seam. The same browser renders a plain clearnet page, or
a [skywire](https://github.com/skycoin/skywire) site addressed by public key
over the dmsg mesh — the host decides, by what it plugs into the fetch.

## Use

The browser is a **library**. There are two ways to host it.

### As a library — compiled into your own wasm (no separate runtime)

A host that already ships a Go/wasm binary (skywire's visor page) imports
netscrape and calls `Open` once. The browser compiles into that binary and
shares its Go runtime — there is no second wasm module and no duplicated ~2 MB
runtime. Set the transport on `globalThis.__netscrapeFetch` first.

```go
//go:build js && wasm
import "github.com/0magnet/netscrape"

netscrape.Open(mountElement) // returns immediately; handlers keep it alive
```

### As a standalone module — served or exec'd as its own wasm

A host that would rather serve the browser as a separate fetch (or a shell that
`run`s it) uses the `dist` subpackage, which carries the pre-built blob + loader:

```go
import "github.com/0magnet/netscrape/dist"

serveBytes("/netscrape.wasm", "application/wasm", dist.BrowserWasm())
serveJS(dist.LoaderJS()) // defines globalThis.Netscrape.open
// or dist.BrowserWasmGz(), to inline the compressed module into a page
```

```js
Netscrape.open(document.getElementById("browser"), {
  wasmURL:       "/netscrape.wasm",
  fetchDmsg:     (pk, method, path, body) => ...,   // → {status, body, headers}
  fetchClearnet: (url, method, body)      => ...,   // → same shape
  // or fetch: (url) => Response   to replace the whole transport
});
```

Either way, mesh hosts (`*.dmsg`, `*.skysocks`, `*.skynet`, a 66-hex public key) route through
`fetchDmsg`; everything else through `fetchClearnet`; absent either, a plain
same-origin `fetch`. The browser reads `globalThis.__netscrapeMount` for its
element and `globalThis.__netscrapeFetch(url)` for every request.

Two optional host hooks: `globalThis.__netscrapeStart` (a URL string) is what a new
tab opens instead of the built-in start page, and `globalThis.__netscrapeStartLinks`
(an array of `{label, url}`) adds shortcuts to that page; the links are read each
time it renders, so a host can set them after `Open`.

`./build.sh` rebuilds `dist/browser.wasm.gz` (embedded by `dist`) from
`cmd/browser`.

## The JavaScript engine

netscrape was a 3.6k-line JavaScript engine (`browse.js`) — a mesh browser with
its own transcoder, a `SkywireBrowse` panel, address-bar channel dispatch, and
window management, grown inside skywire. That version lives on the
[`js` branch](https://github.com/0magnet/netscrape/tree/js). `main` is the Go
rewrite; consumers move over as it reaches parity.

## Related projects

Another browser engine running inside a browser tab:

- [Firefox in WebAssembly](https://developer.puter.com/labs/firefox-wasm/) — Puter's Gecko compiled to WebAssembly, a full browser engine inside a tab

## Dependency Graph

Made with [goda](https://github.com/loov/goda):

```
# GOOS=js: the import edges of a wasm program live in js/wasm-tagged
# files and are invisible to a host-context run
GOOS=js GOARCH=wasm go run github.com/loov/goda@latest graph github.com/0magnet/netscrape/... | dot -Tsvg -o docs/netscrape-goda-graph.svg
```

![Dependency Graph](docs/netscrape-goda-graph.svg "github.com/0magnet/netscrape Dependency Graph")

## Lines of Code

Made with [gocloc](https://github.com/hhatto/gocloc) (excludes `vendor/`, `node_modules/`, `.git/`):

```
gocloc --not-match-d='(vendor|node_modules|\.git)' .
```

```
-------------------------------------------------------------------------------
Language                     files          blank        comment           code
-------------------------------------------------------------------------------
Go                               6            102            427           1260
JavaScript                       2             61             50            520
HTML                             4              1             16            124
Makefile                         1             21             52            111
Markdown                         1             33              0             98
YAML                             1              0              7             98
Bourne Shell                     2              9             25             40
CSS                              1              0              0              6
XML                              1              0              0              4
Plain Text                       1              1              0              3
-------------------------------------------------------------------------------
TOTAL                           20            228            577           2264
-------------------------------------------------------------------------------
```
