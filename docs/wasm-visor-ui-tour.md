# The desk — the wasm-visor UI, a visual tour

The **desk** is a Skywire hypervisor UI compiled to WebAssembly
(`GOOS=js GOARCH=wasm`) that presents the tab as a small workstation: a panel
with an Applications menu, draggable windows, a terminal, a file manager, and a
browser. It is hosted out of the one `skywire` command module, so the terminal
runs real `skywire` subcommands and the browser is a real mesh browser.

You meet it in two places, and the difference matters for almost every window
on it:

- **Standalone** — the tab **is** a visor. It mints its own key, dials into the
  network over dmsg-over-WebSocket / WebTransport / WebRTC, registers in
  discovery and carries real transports. Served by
  `skywire cli hv serve`, or from a visor's own `hypervisor.wasm_serve.addr`.
- **Beside a native visor** — the same desk, served by a native hypervisor at
  the root of `hypervisor.desk_addr` (`:8010`) next to the Angular dashboard on
  `hypervisor.addr` (`:8000`). The tab still runs its own visor, but the
  interesting relationship is that it asks to drive the host as a hypervisor —
  see [pairing a desk tab](guides/hypervisor.md#pairing-a-desk-tab).

> This page used to tour `cmd/wasm-visor`, a separate in-tab hypervisor with
> its own chat, content-hosting, console and log windows. That command was
> removed in #4761 and the desk replaced it; the windows below are the ones
> that exist now.

---

## The desk itself

![the desk](https://github.com/skycoin/skywire/raw/develop/docs/img/desk/01-desk.png)

The panel sits at the bottom: **Applications** on the left, a task button per
open window, the clock on the right. Windows are WinBox frames — drag, resize,
minimize, maximize, full-screen — and one that holds tabbed surfaces puts its
tab strip in the title bar, level with the window controls, rather than
stealing a row of its own.

The Applications menu is a filter box over the registered apps — type to narrow
it — and each entry carries its own one-line help. On a standalone desk:

| app | what it is |
|---|---|
| **browser** | netscrape — browse the mesh and the clearnet |
| **files** | the shared filesystem (jsfs) |
| **identity** | export or import this tab's key |
| **Install Skywire** | install the desk as an app, so it opens on its own and keeps working when this address is out of reach |
| **mail** | e-mail over Skywire: this tab's mailbox |
| **pair** | ask the visor that served this page to accept this tab as a hypervisor |
| **settings** | this tab's `skywire.conf`: what every reload generates the visor's config from |
| **terminal** | websh — the Skywire shell |

Two of them change with where the desk is served. **terminal** is websh on a
standalone desk and the *host's* pty page — xterm over a websocket to the
machine the visor runs on — when a native hypervisor served the page.
**pair** only has something to talk to in that second case; on a standalone
desk there is no host to pair with.

---

## 1. The dashboard is a browser tab

![the dashboard as a netscrape tab](https://github.com/skycoin/skywire/raw/develop/docs/img/desk/02-dashboard-tab.png)

The Angular dashboard is not the desk's frame — it is a page, so the desk opens
it as the first tab of a browser window, addressed as `vnet:8001`: the
hypervisor UI of this tab's own visor, on the in-page virtual loopback.

It renders **natively**, not through the browser's transcoder. netscrape
normally rewrites a page into a sandboxed `srcdoc`, which strips the
same-origin an Angular app needs; `DirectLoader` claims same-origin URLs and
hands back the service-worker path that really serves them, so the tab keeps
the canonical `vnet:8001` in the address bar while the iframe renders out of
`<origin>/vnet/8001/` with no `sandbox` attribute at all.

Its tabs are the ones the native hypervisor serves — Visor list, Local Visor,
Rewards, Resources, Transports, Network, Network Visualizer, Deployment,
Uptime, Settings — driven here by the in-wasm visor core rather than a remote
RPC. The footer names the build, and on a wasm visor it reads
**`wasm hypervisor`**: the tell-tale that the API answering the page is in the
tab, not on a host.

That is the whole of the dashboard's surface. Everything else on this
page — the shell, the file manager, the mailbox, the config editor, the key
import/export, netscrape itself — is a desk window with no dashboard
equivalent. The dashboard is a tab in the desk, not the other way round.

---

## 2. netscrape — the browser

![a mesh site in netscrape](https://github.com/skycoin/skywire/raw/develop/docs/img/desk/03-netscrape-mesh.png)

A browser that resolves by **public key** as readily as by DNS. Its tab strip
lives in the window's title bar; the toolbar has back / forward / reload, an
address bar, **Go**, and a settings button holding the proxy field, which
defaults to `socks5://vnet:4445` — the visor's own resolving proxy on the
virtual loopback. Names go to the proxy and the proxy decides them, exactly as
a browser beside a native visor would.

It addresses:

- `vnet:<port>` — this tab's own services (the dashboard on 8001,
  `skywire doc serve` on 8002)
- `<pk>.dmsg`, `dmsg://<pk>`, `<name>.dmsg` — mesh sites over dmsg
- `<pk>.skynet` — mesh sites over routed skynet
- clearnet URLs, through a skysocks exit

`home.dmsg`, shown above, is the resolver's own page: the services this proxy
can reach by name, each with the public key behind it — the deployment's
`dmsgd.dmsg` and `tpd.dmsg` and the rest, with their API endpoints as links
and fill-in forms that tunnel through the same proxy.

A mesh site does **not** render in a sandbox. The real-origin substrate mints a
per-target origin — `<id>.<suffix>`, where the id is a hash of the canonical
target, because a wildcard certificate matches exactly one label and so cannot
carry the target in the hostname — and the frame loads there. The address bar
still reads `http://home.dmsg/` while the document behind it is at something
like `https://74wqe524hg2qkjaxctxo.theskywirenetwork.net/`: a genuine,
separate origin with its own storage and its own script world, which the desk
that opened it cannot read into. netscrape inverts the rewrite for display, the
same way it keeps `vnet:8001` in the bar for the dashboard. See
[real-origin-browser.md](real-origin-browser.md).

---

## 3. terminal — websh

![websh](https://github.com/skycoin/skywire/raw/develop/docs/img/desk/04-websh.png)

Not a REPL. websh is a Bash/POSIX interpreter over an in-memory filesystem,
rendered by a Go port of xterm.js, with the visor's API as applets whose JSON
output pipes into the shell's own `jq`:

```
about | jq -r '.public_key, .build.version'
tps | jq -r '.[].type' | sort | uniq -c
for pk in $(visors -c | jq -r '.[].local_pk'); do echo "$pk"; done
```

The applets are `about`, `visors`, `net`, `health`, `apps`, `tps`, `routes`,
`pk` and `hvapi` (call any API path directly), alongside `curl`, `dcurl`,
`dial` and `skywire` itself — the whole CLI, in the tab. Pipes, redirection,
loops, `edit` and `less` all work, and the shell exports the visor's resolver
into the environment so `curl` and `got` reach `.dmsg` and `.skynet` without
being told how.

The terminal window tabs: the first console opens it, and every console after
that joins as a tab rather than scattering frames over the desk.

On a standalone desk the visor itself is not in this instance — it is the
`skywire autoconfig` instance in the exec worker, and the applets reach its API
over the virtual loopback, the same way the dashboard tab renders.

---

## 4. files

The jsfs file manager over the **same** filesystem the shell uses: a file
created in the terminal appears here, and the reverse. It is MemMap-backed and
ephemeral — it goes when the tab closes, like the rest of a standalone wasm
visor — with a persisted IndexedDB snapshot where the page asks for one.

---

## 5. mail

The tab's Skymail mailbox: e-mail addressed by public key, carried over
Skywire, with attachments and the size and age limits the visor's settings
define. A visor gets a mailbox whether or not anyone opens this window; the
window is where you read it.

---

## 6. settings

The desk's config editor. It edits this tab's `skywire.conf` — the small set of
values every reload regenerates the visor's full config from — rather than the
generated JSON, which is why a change here survives the next boot instead of
being overwritten by it.

---

## 7. identity, and pairing

**identity** exports or imports this tab's key. A standalone desk mints one on
first visit and persists it in `localStorage` per origin, which is what makes
serving the desk from a public domain safe: the page never asks anyone to type
a secret key, and a new origin is a new visor.

**pair** is the other half of that. A desk tab served by a native hypervisor
runs its own visor and asks to drive the host; approve it on the machine with
`skywire cli visor hv pair`, or mint a one-time code with
`skywire cli visor hv pair --code` and type it into this window. The direction
of that relationship is the thing most often got backwards — the guide has it
in full.

---

## 8. Install Skywire

Installs the desk as a PWA, so it opens on its own and keeps working when the
address that served it is out of reach.

---

## Serving it

```
skywire cli hv serve --tls -a 127.0.0.1:8443
```

builds the single-file page from the embedded wasm module and serves it over
HTTPS; accept the local certificate once and the visor boots in the tab. The
module comes from the binary itself — from the two-stage build (`make
build-embedded`, which every published binary has) or from `--exec-wasm` — so
the served desk is the running `skywire` version, and a plain source build with
no module refuses to serve.

A visor hosts the same surface itself from `hypervisor.wasm_serve.addr`, and a
hypervisor serves it beside the dashboard at `hypervisor.desk_addr` (`:8010`).
See [the two ports](guides/hypervisor.md#the-two-ports).

## Regenerating these screenshots

The desk is a live page, so shoot an existing tab rather than booting another
one — a second wasm visor costs about 700 MB of JS heap:

```
curl -s localhost:9222/json/list | jq -r '.[] | "\(.type) \(.url) \(.webSocketDebuggerUrl)"'
go run ./scripts/tabshot ws://127.0.0.1:9222/devtools/page/<id> /tmp/shot.png
magick /tmp/shot.png -crop 1921x445+0+0 +repage docs/img/desk/<name>.png
```

`skywire cli hv eval <webSocketDebuggerUrl> '<js>'` reads and drives the same
tab for staging — maximizing a window, opening the Applications menu,
`__skywireDesk.openConsole({title, initCmd})` for a shell with a command
already run. Put the desk back as you found it afterwards. Where no browser is
already running, `cmd/hvinspect` boots a headless one and captures console, DOM
and a screenshot together.
