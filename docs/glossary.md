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
- **skynet** — the routed mesh: a PK dialed over skywire *routes*, forwarded
  hop by hop by intermediate visors, as against dmsg which is *relayed* by
  dmsg servers. The two are the alternatives behind `--transport
  auto|dmsg|skynet|tcp`, where `auto` tries skynet first and falls back to
  dmsg. Hostnames under `.skynet` resolve to it. → `pkg/skynet`
- **transport** — a link between two visors. Types: `dmsg`, `stcpr`, `sudph`,
  `stcp`, `squicr` (QUIC), `swsr` (WebSocket), `swtr` (WebTransport),
  `webrtc`; legacy aliases `quic`/`squic`, `ws`, `wt` are still accepted.
  → `pkg/transport/types`
- **transport naming (`s…r`)** — the wire names are built as
  **s**(kywire-Noise) + protocol + suffix. The leading `s` says the
  Noise+yamux session runs over that carrier exactly as it does over every
  other one; a trailing `r` says the peer's address comes from the
  **address-resolver** rather than being pinned locally. So `stcp` is TCP with
  a local PK table, `stcpr` is the same over the resolver, and `squicr`,
  `swsr`, `swtr` are QUIC, WebSocket and WebTransport on the same pattern.
  `sudph` is the exception: UDP + **h**ole punching.
  → `pkg/transport/types/types.go`
- **address resolver (AR)** — the service that maps a PK to a reachable
  address, so a transport can be dialed without knowing an IP in advance.
  What the `r` in `stcpr` refers to.
- **route / route group** — a (possibly multi-hop) forwarding path set up over
  transports; a route group bundles the forward + reverse legs.
- **dead edge** — a transport still present in a route-finder snapshot that no
  longer exists, typically because a hub restart dropped it. Routes are
  checked against the live graph before being handed out, and a transport that
  stops answering pings is drained from TPD rather than left to be routed
  over. → `pkg/deployment/rf/store/landmark.go`
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
- **portless-internal** — an in-process app that binds *no* TCP port, so its
  UI/API is reachable only via the HV / appnet. Opt-in (`--portless`): an
  in-process app normally publishes to the control surface **and** binds its
  own port, and registering with the control surface says "runs in this
  process", not "has no port".

## wasm / PWA

- **wasm visor** — a full visor compiled to WebAssembly, running entirely
  inside a browser tab (no install, no server). → the root binary built for
  `GOOS=js` (`/skywire.wasm`), run by the desk terminal
- **standalone wasm-visor** — the keyless PWA served by `skywire cli hv serve`;
  each visitor's browser mints its own ephemeral key.
- **☰ menu** — the desk-bundle taskbar app menu present on both the native and
  wasm hypervisor UIs; where features (browser, terminal, wallet, about…) open.
  → `pkg/wasmhv/browseui`
- **Go browser** — the mesh browser compiled into the wasm-visor binary
  (`globalThis.skywireBrowser`, netscrape); replaced the retired JavaScript
  browse engine (browse.js / SkywireBrowse). → `pkg/wasmhv/deskhost`

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
- **skysocks-client-lite** — the cut-down socks client the wasm visor's iframe
  browser reaches clearnet through, as against a full skysocks-client app.

## Services (deployment)

- **TPD** — Transport Discovery. **AR** — Address Resolver. **RF** — Route
  Finder. **SD** — Service Discovery. **UT** — Uptime Tracker. **dmsgd** —
  dmsg Discovery. → `deployment/services-config.json`
- **CXO** — content-exchange object store; the gossip/replication layer used by
  TPD feeds, skychat groups, etc. → `pkg/cxo`

## Keys & identity

- **PK / SK** — public key / secret key. A visor *is* its key pair: the PK is
  the address other visors dial, and nothing on the mesh is reached by IP.
  Written as 66 hex characters (a 33-byte compressed secp256k1 point).
  → `pkg/cipher`
- **ephemeral key** — a key pair minted for one process or one browser tab and
  never persisted. What the standalone wasm-visor gives each visitor, and what
  a client gets when `--sk` is not supplied.
- **noise / XK** — the handshake every transport runs once its carrier is up:
  Noise in XK mode, where the dialer already knows the responder's static PK
  and the responder learns the dialer's. This is what makes a PK an address
  rather than a claim. → `pkg/transport/network`
- **whitelist** — the per-service allow-list of PKs permitted to do something
  privileged: run a command over dmsgpty, act as a route setup node, register
  a survey. Not one list — each subsystem keeps its own.

## Remote access

- **dmsgpty** — remote shell and remote command execution over dmsg, keyed by
  PK over a noise-XK connection. The names parallel ssh/sshd/sshfs by analogy
  only; there is no SSH involved. → `skywire cli pty`, `pkg/pty`
- **pty host** — the server side of dmsgpty. Reachable over the dmsg overlay,
  or on a direct TCP port (`:2022` by default, the analogue of sshd's `:22`).

## Config & operations

- **SKYENV** — the environment variable naming the skyenv file, and by
  extension the file itself (`/etc/skywire.conf` by default): a bash-style
  `KEY=value` file that `skywire autoconfig` writes and `cli config gen`
  reads. One level of `SKYENV=` redirect inside the file is honored.
  → `pkg/skywireconfig/skyenvfile`
- **skywire-config.json** — the visor's generated runtime config. A *derived*
  artifact: the skyenv file is the source of truth, and the next autoconfig
  run rebuilds this from it. → [guides/config-gen.md](guides/config-gen.md)
- **survey** — the node-metadata report a visor submits for rewards
  (hardware, version, uptime), gated by `survey_whitelist`. → [reward-system.md](reward-system.md)

## Routing & multiplexing

- **setup node (SN) / route setup node (RSN)** — the node that signs per-hop
  capabilities for a route. A *signing oracle*: it authorizes hops and is
  never on the data path, and targets honor only allow-listed RSNs.
  → `pkg/router/cascade_source.go`
- **mux / multiplexed route** — several parallel routes carrying one logical
  stream, so a flow is not hostage to a single path.
- **mux mode** — how weight is spread across a mux's legs. `auto` weights by
  latency and falls back to round-robin when there is no latency data,
  `equal` is round-robin, and explicit weights come from the routing-policy
  DSL's `distribution="weighted: f1, f2, …"`. Set per app with
  `--mux-mode`. → `pkg/router/transport_selector.go`
- **capacity-weighted selection** — picking a peer at random but in proportion
  to reported capacity rather than uniformly, so a large server takes a large
  share of load instead of every client converging on the same one.
  → `pkg/dmsg/dmsg/client.go`, `pkg/router/policy/distribution.go`
- **mux cap** — the ceiling on how wide the adaptive mux may grow at runtime.
  → `SetMuxCap`

## Bridges & gateways

- **meshgw** — the mesh gateway in the VPN router: registers `.dmsg` and
  `.skynet` DNS zone handlers so those names resolve to leased synthetic IPs,
  letting an unmodified program on the LAN reach the mesh by name.
  → `pkg/vpnrouter/meshgw`
- **skymailbridge** — SMTP over skywire: a minimal server-side state machine
  that reads the peer's PK out of the recipient domain's pre-suffix DNS label
  and dials it over a transport. → `pkg/skymailbridge`
- **skynetca** — the local certificate authority the resolver uses to
  terminate TLS for `*.skynet`, `*.dmsg` and `*.skysocks` names in the
  visitor's browser, so mesh sites get a padlock without a public CA.
  → `pkg/skynetca`
- **skyudpbridge** — UDP carried over a reliable skywire byte stream as
  length-prefixed datagrams ("Plan B" of the UDP-over-skynet split, as against
  true packet-level UDP). → `pkg/skyudpbridge`
- **telemetrywire** — the compact sharded binary codec for the visor→TPD
  telemetry feed over CXO, shared by both the publishing and consuming sides.
  → `pkg/telemetrywire`
- **tpviz** — the transport visualizer: the mesh drawn as a graph, including a
  latency-space view where edge weight is measured round-trip time.
  → `pkg/tpviz`

## Code navigation

- **component tag** — the `c<layer>-<domain>-<area>` marker in the header
  comment of nearly every Go file (`c0-com-util`, `c1-net-dmsg`,
  `c3-vis-core`, `c5-cli-visor`…). One tag per package — files within a
  package are expected to agree, which #4276 went through and enforced by
  hand. The layer runs 0–5, ascending roughly from shared primitives
  (`c0-com-*`) to the CLI surface (`c5-cli-*`); the domain and area say which
  part of the tree a file belongs to. Nothing enforces it automatically, so it
  is a reading aid rather than a guarantee.

## Component libraries

The browser-side visor is assembled from standalone `github.com/0magnet/*`
libraries rather than built into this repository. Each is usable on its own;
what follows is what each one is *for* here.

- **bottle** — the OS layer that lets a Go program run in a browser tab the
  way it runs on Linux. Installs a page-global in-memory filesystem
  (`jsfs.js`) that Go's js/wasm runtime routes the whole `os` package through,
  a Worker bridge for child processes, and **vnet**. → `0magnet/bottle`
- **calvin** — the ASCII-art font renderer behind the banner at the top of a
  help screen. → `0magnet/calvin`
- **cosmos-go** — the GPU force-directed graph (a port of cosmos.gl) the
  transport visualizer draws the mesh with. → `0magnet/cosmos-go`
- **desk** — the window manager and chrome: a taskbar, a ☰ launcher, and panes
  arranged in windows. Deliberately knows nothing about terminals or images —
  a pane is anything that renders into a DOM element. → `0magnet/desk`
- **netscrape** — the nested browser. Chrome (tab strip, address bar,
  back/forward) built with syscall/js, each tab a sandboxed `<iframe>`; a page
  is fetched over a host-supplied transport — clearnet or the dmsg mesh — and
  rendered into a sandboxed srcdoc with its stylesheets and images inlined.
  → `0magnet/netscrape`
- **realorigin** — serves untrusted remote content at a genuine isolated
  browser origin whose network layer is a service worker, keeping the
  credentials that fetched it on a separate origin. → `0magnet/realorigin`
- **spheregraph** — embeds a weighted graph on the unit sphere so great-circle
  distance approximates measured round-trip time. → `0magnet/spheregraph`
- **termanim** — the terminal animations, including the code rain behind the
  help screen. → `0magnet/termanim`
- **vnet** — a virtual loopback network for the tab: a page-global port table
  with in-memory byte pipes, so a wasm instance that LISTENS on
  `127.0.0.1:<port>` can be dialed by another instance in the same page, or by
  page JS. The in-tab equivalent of localhost. `vnet.Listen` / `vnet.DialTimeout`
  are plain `net.Listen` / `net.DialTimeout` on native builds.
  → `0magnet/bottle/vnet`
- **websh** — the browser terminal's shell: a Bash/POSIX interpreter over the
  in-memory filesystem, with the visor's own API exposed as applets whose JSON
  output pipes into `jq`. Replaced a bespoke one-command-per-line JS REPL.
  → `0magnet/websh`
- **winbox-go** — the draggable, resizable windows themselves (a port of
  winbox.js); desk arranges panes inside them. → `0magnet/winbox-go`
- **xterm-go** — the terminal emulator: a Go/wasm port of xterm.js 6.0.0, with
  a pure-Go VT core and a browser layer on top. What `websh` renders into.
  → `0magnet/xterm-go`

<!-- Add terms as they are coined. Keep entries to a sentence or two.
     A term that no longer exists in the code does not belong here; say so in
     the doc that still describes it instead. -->
