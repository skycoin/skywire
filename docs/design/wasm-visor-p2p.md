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
  goal. Options: rely on host candidates only (works when one peer is public,
  e.g. a visor), run a skywire-hosted STUN/TURN, or treat WebRTC as a
  same-network / public-peer optimization and keep dmsg as the universal path.
  The signaling layer is dmsg-native regardless; only the media path needs ICE.
- **TPD trust for ephemeral tabs.** A tab churns more than a visor; TPD edge TTL
  and the finder's liveness weighting (see the dead-edge work) need to tolerate
  high-churn leaf edges without polluting routes for everyone.
- **Carrier vs mesh for WebTransport.** WT is wired as a dmsg *carrier* here. It
  could also back a `webtransport` mesh transport (browser→public-visor direct),
  parallel to WebRTC. Decide whether that earns its keep over WS+routing.
- **Scope of the wasm visor.** How much of `pkg/router` / `pkg/transport` is
  worth compiling into wasm vs. keeping the tab a signaling-and-DataChannel leaf
  that delegates routing to a paired full visor.

## 7. What landed with this design

- `pkg/dmsg/dmsg/ws_js_tinygo.go` — browser WebSocket `net.Conn` dmsg carrier.
- `pkg/dmsg/dmsg/wt_js_tinygo.go` — browser WebTransport `net.Conn` dmsg carrier
  (cert-hash pinned, CA-free), `wt_stub_tinygo.go` for non-browser TinyGo.
- `cmd/dmsg-wasm/webrtc_js.go` — WebRTC DataChannel `net.Conn` + offer/answer/ICE
  state machine over a dmsg `SignalChannel`; JS API `webrtcDial` / `webrtcListen`.

All compile-verified under both standard-Go wasm and TinyGo wasm. None is
runtime-validated in a browser yet — that is the next gate before §4.
