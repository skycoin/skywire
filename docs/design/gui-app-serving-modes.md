# GUI visor-app serving modes (skychat, skycoin-web)

How a visor's GUI apps (skychat, skycoin-web/wallet) are **served to the
operator** and **connect to their backends**, across the wasm visor and the
host-native visor. Written because the config space *looks* like a large
cross-product but collapses to one primary axis plus a couple of knobs.

## The wallet is a FEATURE, not an app

The single most clarifying distinction: **the wallet is a built-in hypervisor
UI feature** — like the browser, the terminal, the network view — reached via
the ☰ menu and the per-node wallet tab. It's a *UI* (client-side Angular +
`skycoin-lite.wasm`, wallets in browser storage) that just needs to be *served*
(the HV serves `/wallet/` same-origin) and a *node* to reach (over dmsg by
default). It is **not a process**, so it does NOT appear in the Apps list and
has no running/stopped status — it's always available.

A **backend wallet service IS an app**: `skycoin-daemon` (the node), or the
full `skycoin-web` *server* if you want its server-side features (disk-wallet
management, server-side multi-coin). These are processes → startable/stoppable
entries in the Apps tab.

So don't read "wallet works but the skycoin-web app is stopped" as a
contradiction: the wallet is the feature (always on); `skycoin-web` in the Apps
list is the optional backend *server* (a different thing that happens to share
the vendored code). native and wasm agree: **Apps list = services only; the
wallet is a feature.**

## The one axis that matters: control-surface vs own-port

An app's UI/API is reached in exactly one of two ways:

- **Control-surface ("serverless")** — reached *through the visor*: the wasm
  visor's in-tab `skywireVisor.*` hooks, or the native HV's reverse-proxy /
  RPC. This is what makes the wasm and host-native visors behave identically,
  and every in-process app offers it.
- **Own-port** — the app binds its own TCP listener (skychat `127.0.0.1:8001`,
  skycoin-web `127.0.0.1:8002`) that the browser/operator reaches directly.

The two are not exclusive. An in-process app publishes its mux to the control
surface *and* may bind a port for the same mux; the HV prefers the in-process
path (no loopback hop, no write deadline on SSE) regardless. Suppressing the
listener is the opt-in — `--portless` for skychat.

"Serverless" here means *no own HTTP port; reached via the visor control
surface* — **not** "no process runs." Two sub-flavors, invisible to the user
but worth naming because they decide how multi-coin works:

- **No process** (wallet, web-only): skycoin-web doesn't run at all. The HV
  serves the static `/wallet/` bundle; wallet crypto is client-side; the
  node API is proxied by the visor over dmsg. Possible only because in
  web-only mode skycoin-web's server does nothing essential.
- **In-process logic** (skychat always; wallet if you want its server for
  `/coin/N` multi-coin discovery): the app's logic runs in the visor process
  (RunModeInternal) and is reached via the control surface — still no port.

## Process model is a *separate*, mostly-invisible choice

Whether the app logic runs in-process or as a child process is about
**privilege and isolation only** — it does **not** decide the port question:

| | internal app | external app |
|---|---|---|
| runs in | the visor process | a child `skywire app …` process |
| app↔visor pipe | `net.Pipe` | unix socket |
| UID | the visor's (root, typically) | `setuid` to `User=` |
| `User=` honored | **no** (no child to setuid) | **yes** |
| own HTTP port | optional knob | optional knob* |
| node transport | appnet (via the visor) | appnet (via the visor) |

\* Today an external app must bind a port to be reachable; the endgame
(below) removes that. Standalone / non-visor-managed skycoin-web is the only
case that *needs* a real port + its own node dialing — it isn't visor-managed,
so there's no control surface to use.

## Knobs

- **serve-on-own-port** (`--skycoinweb-port` style): default **off** → UI only
  via the HV. Meaningful for the app modes; a no-op / error for a mode that
  has no process to listen. The escape hatch for "I want to hit the app
  directly without the HV."
- **wallet storage**: browser (no `--wallet-dir`, the default) or disk
  (`--wallet-dir`). Disk requires a running app; on a root internal app the
  dir lands under root's HOME (`User=` can't drop an internal app — run it
  external to write under a user's HOME).
- **node transport**: visor-managed apps reach the node over **appnet** (the
  visor routes, no SOCKS5 port). The `--socks5-proxy` path still works.
  skycoin-web takes an injected `NodeHTTPClient` so skywire supplies the
  dmsg-backed transport with `net/http` as the only skycoin dependency.
- **multi-coin / BTC**: not an architectural limit — configure additional
  `--node-url`s (fibercoins) or an electrum endpoint (BTC) port-forwarded over
  dmsg/skynet. In the app modes the server does `/coin/N`; in the no-process
  wallet mode the HV proxy / shim must route `/coin/N` → the Nth node. Off by
  default; on after you configure the nodes.

## Defaults

Both apps are reachable through the control surface out of the box, so native
== wasm without configuration:

- **wallet** → **no-process serverless**: HV serves `/wallet/`, proxies the
  node over dmsg, wallets in the browser. Zero config, zero skycoin change.
- **skychat** → **in-process, control-surface + own port**: runs in the visor
  and publishes its mux to the HV, *and* binds `--chataddr` (default
  `127.0.0.1:8001`). The HV path is what the chat tab uses; the port is what
  `skywire cli skychat`, `skywire cli visor doctor`, the e2e suite and a plain
  browser use. `--chatportless` drops the listener and leaves only the HV
  path — worth choosing when nothing local should be able to reach the chat
  app without authenticating to the hypervisor first.

Opt into an **app with own-port** only when you want the full skycoin-web
server (server-side multi-coin, disk wallets) or direct non-HV access.

## Endgame

Give skychat/skycoin-web an **injectable `net.Listener`** (the server-side twin
of `NodeHTTPClient`): the app serves its UI/API over the appnet connection, so
even an **external** app needs no localhost port — every app UI is reached
uniformly through the HV, in every mode. Until then, own-port is how a
non-visor-managed or externally-launched app is reached.

Guiding assumption: if you're using GUI visor apps, the hypervisor UI is
already running — so making app GUIs HV-first (own-port as the exception) costs
nothing in practice and buys same-origin, remote-safe access for free.
