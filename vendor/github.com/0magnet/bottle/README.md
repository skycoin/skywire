# bottle

A Linux-shaped bottle for wasm ships: the OS layer that lets Go programs run
in a browser tab the way they run on a host.

**Live demo** — bottle is the layer underneath rather than a thing to look at,
so its demos are the programs that stand on it:
**[shipwright](https://0magnet.github.io/shipwright/)** runs `cmd/compile` and
`cmd/link` against bottle's jsfs, and
**[shipyard](https://0magnet.github.io/shipyard/)** is a whole workstation on
it — a shell, `go build`, processes and pipes, and a Go server the in-tab
browser fetches from over bottle's vnet. Between them they exercise all three
primitives: the filesystem, the network and the process layer.

Big Go programs assume an operating system: a filesystem under `/`, config in
`/etc`, localhost ports to listen on and dial. A browser tab has none of that,
and Go's `wasm_exec.js` stubs it all with `ENOSYS`. bottle fills the gap with
a few page-global primitives:

- **`jsfs.js`** — an in-memory filesystem, laid out like a Linux root,
  installed as `globalThis.fs` / `globalThis.process` (the exact contract
  `syscall/fs_js.go` calls). Every wasm instance on the page shares it: one
  program writes `/etc/foo.conf`, a shell in another instance `cat`s it.
- **`vnet.js`** — a virtual loopback network: a page-global port table with
  in-memory byte pipes. An `http.Server` or RPC listener bound to
  `127.0.0.1:<port>` in one instance is dialed from another — or from page
  JS via `vnet.httpFetch(port, method, path, body)`.

- **`proc.js`** — a process layer: `proc.spawn({argv, env, cwd, stdio})`
  instantiates another wasm module from jsfs as a child that shares the
  page's fs and vnet, with per-process stdio and an exit promise. A tab has
  no fork/exec, but instantiating a wasm module IS spawning a process — this
  makes that a primitive, the third leg under a Unix-shaped orchestrator
  (a shell, and eventually `go build`). The **`proc`** subpackage is its Go
  adapter (`proc.Command(...).Run()`, os/exec-shaped).
- **`fsbridge.js`** — the same filesystem, reachable from a Worker.
  `proc.spawnWorker` runs a child off the main thread, so a long compile no
  longer freezes the tab, and several can run at once. jsfs stays on the thread
  that owns it; the child blocks in `Atomics.wait` while the page answers,
  which is the synchronous syscall contract Go's runtime requires. Needs
  cross-origin isolation (COOP/COEP) for `SharedArrayBuffer`; without it
  `spawnWorker` refuses and callers fall back to `proc.spawn`.

The **`vnet`** Go subpackage is the adapter: `vnet.Listen` /
`vnet.DialTimeout` are exactly `net.Listen` / `net.DialTimeout` on native
builds, and route loopback addresses through the page table under `js/wasm` —
so the same code serves on a host and in a tab.

## Use

Embed and serve the scripts ahead of any wasm module (Go captures
`globalThis.fs` when an instance starts):

```go
import "github.com/0magnet/bottle"

page := append(bottle.JSFS(), bottle.VNetJS()...) // then your own JS
```

Seed application layout after `jsfs.js` runs, from a page script:

```js
jsfs.mkdirp('/opt/myapp');
jsfs.writeFile('/etc/myapp.conf', 'KEY=value\n');
```

And in the program, listen/dial loopback through the adapter:

```go
import "github.com/0magnet/bottle/vnet"

l, err := vnet.Listen("tcp", "127.0.0.1:8000") // page-shared under js/wasm
c, err := vnet.DialTimeout("tcp", "127.0.0.1:8000", 5*time.Second)
```

Notes:

- `vnet` conns honor `SetReadDeadline` including waking an already-blocked
  Read — `net/http`'s response teardown depends on that.
- In-memory only: page lifetime, no persistence, single JS realm (a
  MessagePort bridge across Workers can layer on later).

Grown in [skycoin/skywire](https://github.com/skycoin/skywire), where the
whole skywire binary runs in the docs-site terminal: `skywire autoconfig` in
one terminal starts a visor in the foreground, `skywire cli` in a second
terminal dials its RPC over vnet, and a nested browser fetches the
hypervisor UI from `http://127.0.0.1:8001` — all inside one tab.

## Used by

- [m2](https://github.com/0magnet/m2) — the store's own web server runs in
  the tab (`serve` in the /desk terminal), listening on the vnet loopback;
  the netscrape browser's second tab reads it back.
- [shipwright](https://github.com/0magnet/shipwright) — the Go toolchain
  compiling, linking and running programs against jsfs, three instances on
  one in-memory disk.
- [websh](https://github.com/0magnet/websh) and
  [tuiwasm](https://github.com/0magnet/tuiwasm) load the layer on their
  pages, so every wasm instance there shares one filesystem and localhost.

## Dependency Graph

Made with [goda](https://github.com/loov/goda):

```
# GOOS=js: the import edges of a wasm program live in js/wasm-tagged
# files and are invisible to a host-context run
GOOS=js GOARCH=wasm go run github.com/loov/goda@latest graph github.com/0magnet/bottle/... | dot -Tsvg -o docs/bottle-goda-graph.svg
```

![Dependency Graph](docs/bottle-goda-graph.svg "github.com/0magnet/bottle Dependency Graph")

## Lines of Code

Made with [gocloc](https://github.com/hhatto/gocloc) (excludes `vendor/`, `node_modules/`, `.git/`):

```
gocloc --not-match-d='(vendor|node_modules|\.git)' .
```

```
-------------------------------------------------------------------------------
Language                     files          blank        comment           code
-------------------------------------------------------------------------------
JavaScript                       4             86            288           1020
Go                               6             60            122            398
YAML                             1              0              7             98
Markdown                         2             22              0             75
HTML                             1              0              2             18
-------------------------------------------------------------------------------
TOTAL                           14            168            419           1609
-------------------------------------------------------------------------------
```
