# bottle

A Linux-shaped bottle for wasm ships: the OS layer that lets Go programs run
in a browser tab the way they run on a host.

Big Go programs assume an operating system: a filesystem under `/`, config in
`/etc`, localhost ports to listen on and dial. A browser tab has none of that,
and Go's `wasm_exec.js` stubs it all with `ENOSYS`. bottle fills the gap with
two page-global primitives:

- **`jsfs.js`** — an in-memory filesystem, laid out like a Linux root,
  installed as `globalThis.fs` / `globalThis.process` (the exact contract
  `syscall/fs_js.go` calls). Every wasm instance on the page shares it: one
  program writes `/etc/foo.conf`, a shell in another instance `cat`s it.
- **`vnet.js`** — a virtual loopback network: a page-global port table with
  in-memory byte pipes. An `http.Server` or RPC listener bound to
  `127.0.0.1:<port>` in one instance is dialed from another — or from page
  JS via `vnet.httpFetch(port, method, path, body)`.

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
