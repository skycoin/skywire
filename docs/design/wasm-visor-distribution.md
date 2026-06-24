# wasm-visor binary: never commit it; distribute it like any other binary

## The problem

The standard-Go `wasm-visor.wasm` (the browser visor, since TinyGo can't do TLS —
see [tinygo issue #3259]) is **~38 MB**, and it changes on every build. Git keeps
every version of a tracked file forever, so committing it — or `go:embed`-ing it
into a tracked package — would add a fresh ~38 MB blob to history on every change,
permanently bloating the repository. We only ever need the *current* build.

## The rule

**The `wasm-visor.wasm` binary is never tracked in git.** Verified policy:

- It builds only into `build/` (`make wasm-visor` → `build/wasm-visor-go/`,
  `make tinygo-wasm-visor` → `build/wasm-visor/`), and `build/` is `.gitignore`d.
- It is **not** `go:embed`-ed anywhere. The single-file hypervisor generator
  (`skywire cli hv gen`) reads the wasm from a `--wasm <path>` flag at generation
  time; it does not bake the binary into the repo.
- `git ls-files '*.wasm'` must never list a `wasm-visor` binary. (It does list a
  few small, intentionally-tracked wasm artifacts — tpviz, doc examples, vendored
  skycoin-lite — which are a separate, pre-existing decision.)

Do **not** add a `//go:embed wasm-visor.wasm` to make `cli hv gen` "just work"
without a build. That is exactly the history-bloat trap.

## How it gets to users instead

The wasm-visor is distributed the same way skywire's other binaries are — built
by the release pipeline, never via git:

- **Release asset** — the release build runs `make wasm-visor` and attaches
  `wasm-visor.wasm` (+ `wasm_exec.js`) to the GitHub release / packages. `cli hv
  gen --wasm <downloaded-path>` consumes it.
- **Package** — the apt/AUR packages can ship the prebuilt wasm under the skywire
  data dir, and `cli hv gen` defaults to that path.
- **Build on demand** — a developer runs `make wasm-visor` and points `--wasm` at
  `build/wasm-visor-go/wasm-visor.wasm`.

If we ever do want a "latest-only, no history" copy reachable by URL, mirror the
apt-repo pattern (`updgithub.sh`): a dedicated artifacts location that is
force-pushed to a single commit (history wiped each update), never the main repo.

## TODO when TinyGo gains software TLS

Once [tinygo issue #3259] is resolved, the small (~2.2 MB) TinyGo build can do
HTTPS again and becomes the default, shrinking the artifact ~17×. Same rule
applies regardless: build it, ship it as an artifact, don't commit it.

[tinygo issue #3259]: https://github.com/skycoin/skywire/issues/3259
