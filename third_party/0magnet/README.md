# third_party/0magnet — Go ports and forks for the browser terminal

Source copies of the Go libraries behind the in-browser terminal (websh) and
its terminal emulator, vendored here rather than imported as modules so the
tree stays self-contained and patchable without a round trip through another
repository.

| directory  | upstream of the copy                  | what it is                                                                   |
| ---------- | ------------------------------------- | ---------------------------------------------------------------------------- |
| `xterm-go` | `github.com/0magnet/xterm-go`         | Go/wasm port of xterm.js 6.0.0 — VT parser + buffers (`vt/`, portable), DOM and WebGL renderers, IME, fit/attach |
| `websh`    | `github.com/0magnet/websh`            | the shell: `sh` interpreter wired to a virtual filesystem, ~45 applets, line editor (`shell/` only; the standalone wasm demo is not copied) |
| `sh/v3`    | `github.com/0magnet/sh` (fork of `mvdan.cc/sh`) | Bash/POSIX parser and interpreter, with js/wasm support           |
| `afero`    | `github.com/0magnet/afero` (fork of `spf13/afero`) | filesystem abstraction, with the `net/http` dependency removed  |
| `u-root`   | `github.com/0magnet/u-root` (fork of `u-root/u-root`) | `pkg/ls` only — long-listing format                          |

## Why forks of `sh`, `afero` and `u-root`

Upstream does not target this environment. The forks carry only what that
costs:

- **`sh`** — the interpreter used `os.Pipe` for pipelines, heredocs and input
  redirects, and `os.Getwd` at startup; neither exists on js/wasm. The runner's
  stdin is now a small interface with build-tagged backends (`os.Pipe` on real
  OSes — unchanged behaviour, subprocess inheritance and read deadlines intact;
  in-process `io.Pipe` on js). Also an `os.SameFile` shim for TinyGo.
- **`afero`** — `HttpFs` pulls in `net/http`, whose js shim does not compile
  under stock TinyGo (the same `roundtrip_js.go` limitation noted in the
  `build-wasm-tinygo` Makefile lane). It is removed, and the `os` functions
  stock TinyGo lacks are shimmed.
- **`u-root`** — `pkg/ls` reads `syscall.Stat_t`, whose shape differs between
  standard Go js, TinyGo js and Linux; it gains variants for each.

Note that `vendor/` also carries the unmodified upstream `mvdan.cc/sh` and
`spf13/afero` as indirect dependencies of other packages. That duplication is
expected: those copies serve their existing consumers, these serve the browser
terminal.

## Re-syncing

The forks are the source of truth and keep their tests (stripped here, as
elsewhere in `third_party/`). To pull changes down:

1. copy the package sources over, minus `go.mod`, tests and testdata;
2. rewrite `github.com/0magnet/<repo>` to
   `github.com/skycoin/skywire/third_party/0magnet/<repo>`;
3. `sh/v3/moreinterp` is deliberately not copied — it wires u-root's entire
   coreutils tree into the interpreter, which websh replaces with its own
   applets.

## Compile gates

`cmd/websh-probe` exists so both wasm lanes keep this code honest; it is
compile-checked by `make build-wasm` and `make build-wasm-tinygo`.

TinyGo builds of these packages work with stock TinyGo 0.41+. The skycoin
TinyGo fork (`github.com/0magnet/tinygo`) also builds them, and additionally
compiles `net/http` for wasm — the limitation that keeps `cmd/wasm-visor` on
the standard-Go lane upstream.
