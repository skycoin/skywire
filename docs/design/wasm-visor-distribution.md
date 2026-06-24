# wasm-visor binary: embed it with the visor, without git-history accumulation

## The problem

The standard-Go `wasm-visor.wasm` (the browser visor, since TinyGo can't do TLS —
see [tinygo issue #3259]) is **~37 MB** and changes on every build. We want it
**shipped inside the skywire binary** (so `cli hv gen` — and, later, the visor
serving the HV UI — produce a browser visor with no external file), but git keeps
every version of a tracked file forever, so committing it or `go:embed`-ing a
*tracked* copy would add a fresh multi-MB blob to history on every change.

## The approach: build-tagged embed of a gitignored binary

The binary is embedded, but the embedded file is **never committed**:

- `make wasm-visor` builds the wasm and writes a gzip of it to
  `pkg/wasmhv/wasmbin/wasm-visor.wasm.gz` (~8 MB). That path is `.gitignore`d.
- `pkg/wasmhv/wasmbin` embeds it **only under the `embedwasm` build tag**
  (`embed_on.go`: `//go:embed wasm-visor.wasm.gz`). Default builds compile
  `embed_off.go` instead and carry nothing — so a plain `go build ./...` (CI,
  lint, dev) neither needs the file nor pays the size.
- `make build-embedwasm` runs `make wasm-visor` then
  `go build -tags embedwasm` — this is the build that ships the wasm-visor inside
  skywire (~8 MB larger).

Because the `.wasm.gz` is gitignored and produced at build time, **git history
never accumulates the binary** — only the tiny source files in `wasmbin/` are
tracked. Each build simply overwrites the local gz.

Do **not** commit `pkg/wasmhv/wasmbin/wasm-visor.wasm.gz` (it's gitignored for
this reason) or change the embed to a non-tag-gated `go:embed` of a tracked file.

## Consuming it

`skywire cli hv gen` sources the wasm in this order:

1. `--wasm <path>` if given (e.g. a TinyGo build, which also needs `--wasm-exec`);
2. otherwise the **embedded** std-Go wasm-visor (`wasmbin.Embedded()`), which uses
   Go's embedded `wasm_exec.js` — no `--wasm-exec` needed;
3. otherwise an error telling you to pass `--wasm` or build `-tags embedwasm`.

So a release-built skywire (`make build-embedwasm`) runs `cli hv gen` with no
flags. A default-built skywire still works via `--wasm`.

## Release / packaging

Build the shipped skywire with `make build-embedwasm` so the wasm-visor travels
with the binary. The `.wasm.gz` is a build artifact, not a committed file; the
release pipeline produces it via `make wasm-visor` as part of `build-embedwasm`.

## TODO when TinyGo gains software TLS

Once [tinygo issue #3259] is resolved, the small (~2.2 MB) TinyGo build can do
HTTPS again and becomes the default embed, shrinking the addition ~4×. The
mechanism is unchanged: build it, gzip it to the gitignored embed path, embed
under the tag.

[tinygo issue #3259]: https://github.com/skycoin/skywire/issues/3259
