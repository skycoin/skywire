# wasm-visor binary: embedded by default, blob updated intentionally

## What ships

The standard-Go `wasm-visor.wasm` (the browser visor — TinyGo can't do TLS, see
[tinygo issue #3259]) is **embedded in the skywire binary by default**, gzipped:
`pkg/wasmhv/wasmbin/wasm-visor.wasm.gz` (~8 MB, from the ~37 MB wasm) is
**committed** and `go:embed`-ed with no build tag. So every build — `make build`,
the release pipeline, and a plain `go install github.com/skycoin/skywire@…` —
carries a working wasm-visor, and `skywire cli hv gen` produces a browser visor
with no `--wasm` flag.

`cli hv gen` sources the wasm as: `--wasm <path>` if given (e.g. a TinyGo build,
which also needs `--wasm-exec`) → otherwise the embedded std-Go wasm-visor (uses
Go's embedded `wasm_exec.js`, no `--wasm-exec`).

## Keeping the blob from churning

The committed `.gz` is **only updated on purpose**:

- `make wasm-visor` builds the wasm into `build/wasm-visor-go/` and does **not**
  touch the committed blob — so day-to-day builds and `go test ./...` never dirty
  it.
- `make embed-wasm-visor` rebuilds and **deterministically** (`gzip -9 -n`,
  stripping name+mtime) writes the committed `pkg/wasmhv/wasmbin/wasm-visor.wasm.gz`.
  Run it when you intend to bump the embedded visor, then `git add` + commit that
  blob deliberately.

So git history gains a new ~8 MB blob only on a deliberate embed update, not on
every code change.

## Pruning old blobs from history (manual, occasional)

Updating the embedded wasm replaces the blob; the **previous** versions stay in
git history and add up over time. To reclaim that space, periodically prune the
historical versions (keeping the current one) with `git filter-repo`:

    scripts/prune-wasm-embed-history.sh      # see the script for the exact steps

**Important caveats** — this is a history rewrite, so:

- It changes commit hashes → it requires a coordinated **force-push**, and every
  clone/fork must re-clone or hard-reset. Don't do it casually on a shared branch.
- The **Go module proxy + sumdb retain already-published versions immutably**.
  `go install …@v1.3.x` for a version that shipped the blob still fetches that
  blob from the proxy regardless of pruning. Pruning shrinks the *git repo* and
  fresh clones, not what the proxy serves for past releases. (One more reason to
  bump the embedded blob infrequently — each tagged release that changes it is
  permanent in the proxy.)

## TODO when TinyGo gains software TLS

Once [tinygo issue #3259] lands, the ~2.2 MB TinyGo build can do HTTPS and becomes
the default embed, cutting the committed blob ~4×. Same mechanism: `make
embed-wasm-visor`, commit intentionally, prune occasionally.

[tinygo issue #3259]: https://github.com/skycoin/skywire/issues/3259
