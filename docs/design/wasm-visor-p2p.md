# Browser p2p transports and the wasm visor

Status: design + foundation landed (browser WebSocket/WebTransport dmsg carriers
and a WebRTC DataChannel `net.Conn` over dmsg signaling — all compile under
TinyGo wasm). The mesh-routing integration described in §4–6 is not built yet.

## 0. The reframing: reachability ≠ listening

A browser tab cannot `accept()` an inbound connection — there is no listen
socket in the sandbox. The intuitive conclusion is "a tab can't be a server."
That conclusion is **wrong for skywire**, because skywire already decouples
*reachability* from *listening*:

- A **dmsg client** never listens. It dials *out* to a dmsg server. Once that
  session exists, other clients dial it **by public key** and the server bridges
  the stream back down the existing session. The client is inbound-reachable
  without ever accepting a socket.
- The same is true one layer up, at the **skywire transport + routing** layer. A
  transport is *bidirectional once established* — it does not matter which side
  dialed. If a node forms one outbound transport to a well-connected peer and
  that transport edge is **registered in the Transport Discovery (TPD)**, the
  route finder can compute routes *through* that peer to the node, and route
  setup installs forwarding rules over the already-open transport. The node then
  serves skynet apps (skychat, skysocks, a website on port 80, …) reachable by
  any visor that can find a route — even though it never listened.

So: a wasm tab that (a) connects out over a browser transport and (b) publishes
its transport edge is a **first-class routable skywire node**. This is the dmsg
model, generalized to the routing layer. The novelty is real — a website or
service hosted from a browser tab, addressed by public key, reachable across the
mesh, with no port, no domain, no inbound socket.

## 1. The one true constraint

What a tab *cannot* have is an entry that others can find **without the tab first
establishing the outbound link**. Reachability is contingent on the tab being
online and holding at least one transport whose edge is in TPD. The moment it
drops, its TPD edges go stale and routes through it fail — exactly like a dmsg
client dropping its session. That is a liveness property, not an addressing
impossibility. (Cf. the dead-edge route-setup work: stale TPD edges are already a
known failure mode the finder must weather.)

Note what is **not** the mechanism: the **Address Resolver (AR)** maps
`PK → IP:port` for hole-punched direct transports (sudph/stcpr). A tab has no
IP:port to advertise, so AR is irrelevant to it. A tab's reachability comes
purely from **TPD edge + routing**, the same way a dmsg client's comes from the
discovery entry + server bridging.

## 2. The browser transport menu

Gated by the sandbox, not the compiler (TinyGo vs Go only changes binary size):

| transport | reaches | role | status |
|---|---|---|---|
| WebSocket | a dmsg **server** | dmsg session carrier | ✅ `ws_js_tinygo.go` |
| WebTransport (HTTP/3) | a dmsg **server** | dmsg session carrier, CA-free (cert-hash pin) | ✅ `wt_js_tinygo.go` |
| WebRTC DataChannel | a **peer** (tab/visor) | true p2p link | ⚙️ `cmd/dmsg-wasm/webrtc_js.go` (foundation) |
| raw TCP (stcp/stcpr) | — | — | ❌ no raw sockets |
| raw UDP (sudph/KCP) | — | — | ❌ no raw sockets |

WebSocket/WebTransport make the tab a dmsg **leaf** (reachable via server
bridging — §0 bullet 1). WebRTC is the one that makes the tab a true **mesh
peer** with a direct link — the building block for §4.

## 3. WebRTC needs a signaling plane — and dmsg is it

WebRTC can't bootstrap itself: the two peers must exchange an SDP offer/answer
and ICE candidates *before* the direct DataChannel exists. They already share
dmsg connectivity, so **dmsg is the signaling channel**: the offer/answer/
candidates ride a dmsg stream (`SignalChannel`, backed by `*dmsg.Stream` on a
dedicated signaling port). Once the DataChannel opens it is adapted to a
`net.Conn` and carries a Noise+yamux session like any other carrier — the same
upper stack, over a browser-native p2p pipe.

This is the clean separation: **dmsg = signaling + fallback relay; WebRTC =
direct data path.** A tab that can't form a direct DataChannel (symmetric NAT,
no TURN) still talks over dmsg; WebRTC is an *upgrade*, never a prerequisite.

## 4. From carrier to mesh transport (not built)

The foundation above gives browser `net.Conn`s. Turning WebRTC into a real
skywire **mesh transport** (so routes can traverse a tab) means:

1. A `network.Type` `"webrtc"` + a transport factory in `pkg/transport` that
   dials via the signaling handshake and yields the `net.Conn`.
2. Wiring it into the transport manager so a tab-visor `SaveTransport` registers
   the edge `(tabPK, peerPK, "webrtc")` in **TPD**.
3. A **route-setup responder** in the tab: accept setup packets over its
   transport(s) and install forwarding rules (this is what lets routes *end* at
   the tab and lets it be an intermediate hop).
4. App hosting: a minimal app server so the tab can answer on app ports
   (a website, skychat, an API) addressed by its PK.

Items 3–4 are the bulk of a "wasm visor": today `cmd/dmsg-wasm` is a dmsg leaf +
in-wasm hypervisor; a wasm *visor* additionally runs the router, transport
manager, and route-setup responder.

## 5. Why this is worth doing

- **Serverless services addressed by key.** Host a site/app from a tab, reachable
  mesh-wide by PK — no host, no domain, no inbound port. The standalone
  hypervisor already proves the "serve a UI from a tab over dmsg" shape; this
  generalizes it to arbitrary routed services.
- **Direct p2p where it matters.** Browser↔browser and browser↔visor DataChannels
  keep payload off the relays — bandwidth and latency win, and a privacy win
  (the relay sees signaling, not data).
- **A genuinely portable visor.** The same reflection-light core that compiles to
  TinyGo wasm and (per the IoT port) to wasip1/microcontrollers becomes a visor
  that runs anywhere a JS engine or a WASM runtime does.

## 6. Open questions

- **ICE servers.** Browser↔browser through NAT needs STUN, and symmetric NAT
  needs TURN — both are clearnet dependencies that cut against the no-clearnet
  goal. Skywire already ships STUN servers (the deployment's `StunServers`, used
  for sudph NAT detection); WebRTC reuses them as ICE servers, so no third-party
  STUN. Options for the harder cases: rely on host candidates only (works when one
  peer is public, e.g. a visor), run a skywire-hosted TURN, or treat WebRTC as a
  same-network / public-peer optimization and keep dmsg as the universal path.
  The signaling layer is dmsg-native regardless; only the media path needs ICE.
- **Config source = the VISOR's config, not the embedded default.** The test
  harness (cmd/dmsg-wasm) exposes `deployment.Prod.StunServers` — the embedded
  defaults. But when the UI is generated/embedded BY a visor (the standalone-HV
  generator, `cli hv gen`), it must inject THAT visor's runtime config — its
  configured STUN servers, dmsg servers, discovery, and service URLs — which may
  differ on custom/private deployments. The generator already inlines config; the
  STUN/ICE config (and any other deployment-specific values) rides along.
  See the `TODO(wasm-visor)` in cmd/dmsg-wasm/main.go.
- **TPD trust for ephemeral tabs.** A tab churns more than a visor; TPD edge TTL
  and the finder's liveness weighting (see the dead-edge work) need to tolerate
  high-churn leaf edges without polluting routes for everyone.
- **Carrier vs mesh for WebTransport.** WT is wired as a dmsg *carrier* here. It
  could also back a `webtransport` mesh transport (browser→public-visor direct),
  parallel to WebRTC. Decide whether that earns its keep over WS+routing.
- **Scope of the wasm visor.** How much of `pkg/router` / `pkg/transport` is
  worth compiling into wasm vs. keeping the tab a signaling-and-DataChannel leaf
  that delegates routing to a paired full visor.

## 7. How much of the visor ports? (measured frontier)

Measured with `go list -deps -tags tinygo` (GOOS=js) and confirmed by
`tinygo build ./cmd/wasm-visor-probe`. The recurring blockers are a small set:
**quic-go** (the raw-socket networks), **net/http** (RF/TPD/AR discovery
clients), **net/rpc** (app-event + RSN cascade), and **os/exec** (app
subprocesses).

| package | blockers | note |
|---|---|---|
| `pkg/routing` | ✅ none | routing rules/types — **compiles under TinyGo today** |
| `pkg/visor/visorconfig` | ✅ none | config + keyring — **compiles today** |
| `pkg/transport/network` | quic-go | per-network files; browser needs only `dmsg.go` |
| `pkg/app/appevent` | net/rpc | app↔visor event channel |
| `pkg/router` | quic-go, net/http, net/rpc | finder client + RSN cascade + the networks |
| `pkg/transport` | quic-go, net/http, net/rpc | TPD client + the networks |
| `pkg/app/appnet` | quic-go, net/http, net/rpc | |
| `pkg/visor` | + os/exec | the app-**subprocess** model is the deepest gap |

`cmd/wasm-visor-probe` is the living frontier check — add imports as packages
port; what `tinygo build` accepts is what's portable.

### Phased plan

1. **Networks: dmsg-only browser build.** Split `pkg/transport/network` so the
   browser build compiles `dmsg.go` (+ the future `webrtc`) and tags out the
   raw-socket networks. Concretely (measured): quic-go enters this package only
   via `quic.go` + `quic_identity.go`; the cascade is `network.go`'s factory,
   which constructs the per-network clients (stcp/stcpr/sudph/quic via raw
   net.Listen/Dial + skyquic). The split: tag `quic.go`/`quic_identity.go`/
   `stcp.go`/`stcpr.go`/`sudph.go`/`stun_client.go`/`tcp_liveness.go` `!tinygo`,
   and split `network.go`'s factory into a native (all networks) + tinygo
   (dmsg-only) variant. Native is unaffected (the tagged files stay in native
   builds) so this is a COMPILE-time refactor, not a runtime change — iterate the
   tinygo build until it accepts the dmsg-only factory. This drops quic-go from
   `pkg/transport` and `pkg/router`. NOTE (measured): the cascade reaches
   `client.go`'s core `Config` struct (embeds `stcp.PKTable`) and the
   `makeClient` type-switch, so a clean split likely wants the per-network
   constructors behind a small interface/registry (register dmsg untagged,
   stcp/stcpr/sudph/quic in `!tinygo` init) rather than a giant tagged switch —
   an invasive but mechanical restructure of core transport code. Do it as its
   own reviewed PR, validated with the autonomous browser harness.
2. **net/http-free service clients.** RF, TPD, and AR each talk HTTP to a service;
   port them over dmsg with `dmsgclient.FetchOverDmsg` (the disc client pattern),
   behind native/tinygo tags. Drops net/http from transport + router.
3. **net/rpc.** Tag out (or gob-RPC, à la wasmhv) `appevent`'s RPC channel and
   `router/cascade_source.go`'s RSN oracle for the browser build. With 1–3,
   `pkg/transport` and `pkg/router` should hit the probe.
4. **In-process apps.** Not a fork — the launcher ALREADY has two modes:
   `RunModeInternal` (`appserver.Proc.startInProcess`, app run-func in a goroutine,
   no subprocess) and `RunModeExternal` (`startExternal`, the os/exec path). A
   browser visor simply uses the internal launcher and tags out `startExternal`,
   which is the only os/exec in `pkg/visor`. The real question is not *how* to run
   apps in-process but *which* apps can do their job inside the browser sandbox —
   see §8. (This dovetails with the unified-app-framework direction, #2775.)
5. **Persistence + glue.** Config/transport-log/route-store over a browser store
   (localStorage/IndexedDB via syscall/js) instead of the filesystem; assemble a
   `cmd/wasm-visor` that wires router + transport-manager(dmsg/webrtc) + route-setup
   responder + in-process apps.

Phases 1–3 are mechanical (tag splits + the proven HTTP-over-dmsg pattern) and get
a **routing+transport core** into the browser. Phase 4 reuses the existing internal
launcher. Phase 5 makes it a visor.

## 8. Which apps fit a wasm visor?

The governing rule: **an app works in a wasm visor iff everything it does is either
pure skywire mesh I/O (dmsg/routes) or a capability the browser sandbox exposes**
(fetch, IndexedDB, WebCrypto, File API, WebRTC, notifications, media). It fails the
moment it needs a raw socket, a TUN device, a local listen port, the filesystem,
or a subprocess.

| app | in a tab? | why |
|---|---|---|
| skychat | ✅ | pure messaging over routes + UI — the canonical fit |
| content / site hosting | ✅ | serve content the tab holds (embedded / IndexedDB / File API) on app port 80, addressed by PK |
| file share | ✅ | serve user-picked files (File API) to peers — personal, ephemeral, key-addressed |
| wallet / skycoin-over-dmsg | ✅ | WebCrypto keys + IndexedDB + dmsg |
| pubsub / CXO data sharing | ✅ likely | gob/reflection, which TinyGo 0.41 handles — needs a compile check |
| capability bridge | ✅ novel | expose a browser-only power over skywire by PK: WebRTC signaling relay, WebCrypto signing oracle, notifications |
| skysocks (egress proxy) | ❌ | needs arbitrary outbound TCP; a CORS-crippled fetch()-relay isn't a real proxy |
| skysocks-client | ❌ | needs a local SOCKS listen port for local apps — a tab has neither |
| VPN server/client | ❌ | TUN device + raw packets — categorically impossible |
| port forwarding | ❌ | "local port" doesn't exist in a tab (the serving direction collapses into content hosting) |

The unifying frame: **a wasm visor is a personal, present-when-you-are node, not
always-on infrastructure.** The apps that fit cluster into four personal verbs —
**communicate** (chat, receive, file-share), **publish** (host your site/profile,
addressed by your key, no host/domain), **transact** (wallet/skycoin over dmsg),
and **expose** (make a browser-only capability reachable by PK).

The clean corollary that makes the limitation principled rather than sad: **a wasm
visor can be a *consumer* of infrastructure apps (proxy, exit, VPN) via routes — it
just can't be a *provider* of them.** Providers need raw sockets/devices the
sandbox forbids; consumers only need routes, which it has. Network *infrastructure*
stays on real visors; the browser becomes a first-class *participant and publisher*.

Two caveats that shape what's worth building first:

1. **Liveness.** These are presence-based services (alive only while the tab is
   open) — great for chat/personal/interactive; for a 24/7 site you'd pair the tab
   with a persistent visor or pin the content elsewhere. Same TPD-edge-goes-stale
   property as §1.
2. **Adoption.** The prize is that a *user* becomes a full skywire participant just
   by opening a page — no install, identity by key, chat + publish + wallet. That
   argues for **skychat + content hosting first** as the proof (both ✅, both
   immediately useful), and tells phase-4's internal-app set to start with those,
   not the proxy/VPN family.

## 8a. Known issue: seeded wasm client doesn't register in discovery

Browser validation found that the TinyGo wasm client **connects to a dmsg server
(session established) but does NOT register its entry in dmsg-discovery** — a
lookup of its own PK returns 404. Peers still reach it via the **connected-servers
fallback** (dmsg-100: a peer sharing a dmsg server is bridged without a discovery
entry), which is why dmsg dial/listen + WebRTC signaling all worked between two
tabs. But arbitrary peers that don't share a server can't resolve it.

The dmsg client's `updateClientEntryLoop` → `EntityCommon.updateClientEntry` →
`PutEntry` runs once at connect (periodic retry only every 5 min,
`DefaultUpdateInterval*5`), so the initial registration via the net/http-free
`dmsgDiscClient.PutEntry` is failing silently. To investigate: instrument
`updateClientEntry` + `dmsgDiscClient.PutEntry`/`PostEntry`; likely the POST over
dmsg, the entry signing, or an `Entry()` precheck under TinyGo. Not blocking
(fallback covers shared-server reachability) but required for register-and-be-
found-by-anyone reachability.

## 9. What landed with this design

- `pkg/dmsg/dmsg/ws_js_tinygo.go` — browser WebSocket `net.Conn` dmsg carrier.
- `pkg/dmsg/dmsg/wt_js_tinygo.go` — browser WebTransport `net.Conn` dmsg carrier
  (cert-hash pinned, CA-free), `wt_stub_tinygo.go` for non-browser TinyGo.
- `cmd/dmsg-wasm/webrtc_js.go` — WebRTC DataChannel `net.Conn` + offer/answer/ICE
  state machine over a dmsg `SignalChannel`; JS API `webrtcDial` / `webrtcListen`.

All compile-verified under both standard-Go wasm and TinyGo wasm. None is
runtime-validated in a browser yet — that is the next gate before §4.
