# bottle

A Linux-shaped bottle for wasm ships: the OS layer that lets Go programs run
in a browser tab the way they run on a host.

**Live demo** — bottle is the layer underneath rather than a thing to look at,
so its demos are the programs that stand on it:
**[shipwright](https://0magnet.github.io/shipwright/)** runs `cmd/compile` and
`cmd/link` against bottle's jsfs, and
**[shipyard](https://shipyard.magnetosphere.net/)** is a whole workstation on
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
  page's fs and vnet, with per-process stdio, a process id, `kill()` and an
  exit promise. A tab has no fork/exec, but instantiating a wasm module IS
  spawning a process — this makes that a primitive, the third leg under a
  Unix-shaped orchestrator (a shell, and eventually `go build`). It also does
  the bookkeeping a process layer owes the other two legs: an exited child's
  vnet claims are released for it (a dead program cannot unlisten its ports,
  and a zombie entry fakes liveness and holds the port against a rebind), and
  `proc.registerURL(path, url)` binds a program too large to hold as bytes to
  its path, streaming it into the compiler on first spawn instead of through
  jsfs. `opts.tail` keeps a per-process stderr ring in `proc.tails`, so a
  crashed child's last words survive it. A child can be a terminal program:
  `stdin: "pipe"` gives it a stdin it waits on (`handle.stdin.write`), and
  `tty: {cols, rows, onRaw}` gives it a size, raw mode and resizes
  (`proc.tty(id)` in the child, `proc.resize(id, cols, rows)` in the parent).
  `kill()` uses the child's own interrupt handler when it has one and
  otherwise stops it outright (exit 130). The loader is chosen by the
  module's imports: Go's `wasm_exec.js` or TinyGo's, from `proc.assets`
  (`wasmExecGo`, `wasmExecTinyGo`). A module is compiled once per path when
  spawned with `opts.stamp` (any string that changes when the program does);
  `proc.cached(path, stamp)` says whether a spawn needs no bytes at all.
  The **`proc`** subpackage is its Go adapter: `proc.Command(...).Run()` or
  `Start()`, os/exec-shaped, with `Kill` and `Resize`. `Cmd.Program` and
  `Cmd.Stamp` carry a program kept outside jsfs and compile it once, and
  `proc.Cached(path, stamp)` reports whether that is already done. On the
  child's side `proc.Term()` returns a `Terminal` (`Size`, `SetRaw`,
  `OnResize`, `Read`, `Write`), and `proc.Getenv` and `proc.Args` stand in for
  `os.Getenv` and `os.Args`, which TinyGo does not fill for a js program, by
  asking proc. See [example/proc](example/proc) for a parent and child.
- **`fsbridge.js`** — the same filesystem, reachable from a Worker.
  `proc.spawnWorker` runs a child off the main thread, so a long compile no
  longer freezes the tab, and several can run at once. jsfs stays on the thread
  that owns it; the child blocks in `Atomics.wait` while the page answers,
  which is the synchronous syscall contract Go's runtime requires. Needs
  cross-origin isolation (COOP/COEP) for `SharedArrayBuffer`; without it
  `spawnWorker` refuses and callers fall back to `proc.spawn`;
  `proc.OffThreadAvailable()` says which to expect, and `Cmd.OffThread` in the
  Go adapter falls back on its own.
- **`vnet-sw.js`** — a service worker that turns vnet ports into same-origin
  URLs, `/vnet/<port>/...`, so an iframe can load an in-page server with
  native resolution. The page calls `vnet.enableSW()`; `bottle.VNetSWJS()`
  serves the worker.
- **`coi-sw.js`** and **`coi-register.js`** — a service worker that re-serves
  the page with COOP and COEP headers, for a host such as GitHub Pages that
  cannot set them, so `SharedArrayBuffer` and `spawnWorker` work. The register
  script reloads once after the worker takes control. `bottle.COISWJS()` and
  `bottle.COIRegisterJS()` serve them.

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
page = append(page, bottle.ProcJS()...)            // process layer
// bottle.FSBridgeJS() is served as a file beside proc.js; the worker loads it by URL
```

Seed application layout after `jsfs.js` runs, from a page script:

```js
jsfs.mkdirp('/opt/myapp');
jsfs.writeFile('/etc/myapp.conf', 'KEY=value\n');
```

Hand a subtree to another filesystem, such as a remote one, with
`jsfs.mount(prefix, provider)`. The provider answers the fs calls under the
prefix with node-style callbacks, whenever it is ready; `jsfs.js` documents
them where the mount layer is defined. A mount at `/` takes the whole tree, as
when another thread owns the filesystem. `jsfs.unmount(prefix)` detaches it.

`jsfs.sync` has the same fs methods without callbacks. Each returns its result
or throws an error with a `.code`, for a loader that cannot wait, such as
TinyGo's, whose WASI filesystem calls are synchronous. Mounted paths answer
`ENOTSUP` there, and a lazy file answers `EAGAIN` until its bytes arrive.
`jsfs.readFileAsync(path)` reads a whole file, mounts included, and proc uses
it to run a program that lives under a mount.

And in the program, listen/dial loopback through the adapter:

```go
import "github.com/0magnet/bottle/vnet"

l, err := vnet.Listen("tcp", "127.0.0.1:8000") // page-shared under js/wasm
c, err := vnet.DialTimeout("tcp", "127.0.0.1:8000", 5*time.Second)
```

Notes:

- `vnet` conns honor `SetReadDeadline` including waking an already-blocked
  Read — `net/http`'s response teardown depends on that.
- Memory first: the filesystem lives for the page, and one JS realm owns it.
  `jsfs.persist.enable(db)` restores and then auto-saves a snapshot in
  IndexedDB, so configs and user files survive a reload. Workers reach the
  same filesystem through `fsbridge.js`.

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

## Related projects

Other operating-system layers for WebAssembly in the browser:

- [Wanix](https://wanix.dev/) — WebAssembly-native Unix with Plan 9-style namespaces and 9P
- [Kandelo](https://kandelo.dev/20260819-demo/) — a POSIX-compatible multi-process WebAssembly kernel for the browser
- [linux-wasm](https://github.com/joelseverin/linux-wasm) — the Linux kernel ported to a WebAssembly architecture, one web worker per task
- [exaequOS](https://www.exaequos.com/blog_wasm_wasi_compilers_in_exaequos.html) — a WebAssembly microkernel OS with WASI compilers in the browser

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
JavaScript                       7            208            829           2473
Go                              17            146            304           1426
Markdown                         2             36              0            168
Makefile                         1             21             52            111
YAML                             1              0              7             98
HTML                             1              0              2             18
-------------------------------------------------------------------------------
TOTAL                           29            411           1194           4294
-------------------------------------------------------------------------------
```
