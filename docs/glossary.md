# Glossary

Shared vocabulary for Skywire — the terms we use in code, CLI, and design docs
that aren't obvious from the names alone. Many are neologisms coined as the
codebase evolved; this is the reference for "what we mean when we say X."

> Contributing: keep entries short (a sentence or two), link to code/docs with
> `→`, and add a term the first time you coin one in a PR. Alphabetical within
> each section.

## Mesh & transport

- **dmsg** — the encrypted, public-key-addressed peer-to-peer messaging layer
  every visor speaks. Noise-encrypted streams relayed by dmsg *servers*; the
  substrate everything else rides on. → `pkg/dmsg`
- **transport** — a link between two visors. Types: `dmsg`, `stcpr`, `sudph`,
  `ws`, `wt` (WebTransport), `webrtc`. → `pkg/transport`
- **route / route group** — a (possibly multi-hop) forwarding path set up over
  transports; a route group bundles the forward + reverse legs.
- **appnet** — the app-networking layer: apps don't dial the network directly,
  the visor *routes for them* over the app protocol. → `pkg/app/appnet`
- **resolving proxy** — the embedded SOCKS5 proxy (`dmsgweb` :4445 /
  `skynetweb` :4446) that resolves `<name>.dmsg` / `.skynet` hostnames and
  dials them over the mesh. → `pkg/dmsgweb`, `pkg/skynetweb`
- **coin backend / dmsg backend** — any service reached over dmsg by
  `<pk>:<port>` (a coin node, a wallet service, an Electrum server…). The
  wallet's node config is really a *backend* config.

## Visor & hypervisor

- **visor** — a Skywire node: a routing peer on the mesh. Runs apps, holds
  transports, originates/forwards routes.
- **visorcore** — the shared, platform-neutral visor assembly so the native
  visor and the wasm visor stop diverging. → `pkg/visor/visorcore`
- **hypervisor (HV)** — the management control-plane + web UI over one or more
  visors.
- **HV-served** — an app UI served *by the hypervisor* at a same-origin path
  (e.g. `/wallet/`), proxying its backend over the visor's dmsg client. No app
  process, no listening port. → `docs/design/gui-app-serving-modes.md`
- **control-surface vs own-port** — the axis for how an app UI is reached:
  through the visor (control-surface / "serverless") vs. the app binding its
  own TCP port. See the serving-modes doc.
- **serverless** — reached via the visor control surface, **no own HTTP port**
  — *not* "no process." skychat is serverless on the wasm visor even though its
  logic runs.
- **portless-internal** — an in-process app whose UI/API is reached via the HV
  / appnet rather than a TCP port.

## wasm / PWA

- **wasm visor** — a full visor compiled to WebAssembly, running entirely
  inside a browser tab (no install, no server). → `cmd/wasm-visor`
- **standalone wasm-visor** — the keyless PWA served by `skywire cli hv serve`;
  each visitor's browser mints its own ephemeral key.
- **☰ menu** — the browse.js taskbar app menu present on both the native and
  wasm hypervisor UIs; where features (browser, terminal, wallet, about…) open.

## Apps & wallet

- **wallet (the feature)** — a built-in hypervisor UI feature (served
  `/wallet/`, reached via the ☰ menu / wallet tab). A *UI*, not a process —
  client-side crypto, browser-storage wallets by default. Not in the Apps list.
- **backend wallet service** — a full `skycoin-web` *server* (`--wallet-dir`):
  an actual app/process that manages wallets server-side. Point the wallet
  feature's backend at one (over dmsg) for server-side wallets instead of
  browser storage.
- **skycoin-web** — vendored thin-client wallet code. Serves a static UI *and*
  can run as a server; the UI is "the wallet" (feature), the server is the
  "backend wallet service" (app). → `github.com/skycoin/skycoin/cmd/skycoin-web`
- **skycoin-daemon** — a fibercoin node process (the blockchain backend the
  wallet queries).

## Services (deployment)

- **TPD** — Transport Discovery. **AR** — Address Resolver. **RF** — Route
  Finder. **SD** — Service Discovery. **UT** — Uptime Tracker. **dmsgd** —
  dmsg Discovery. → `deployment/services-config.json`
- **CXO** — content-exchange object store; the gossip/replication layer used by
  TPD feeds, skychat groups, etc. → `pkg/cxo`

<!-- Add terms as they're coined. Placeholders to fill in: skysocks-client-lite,
     mux-auto GROW, dead-edge exclusion, capacity-weighted selection, … -->
