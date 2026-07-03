# RFC: Skynet routing control & multihop route reuse

Status: draft · Author: operator + Claude · Relates to: skysocks-client-lite, the
resolving SOCKS5 proxy, skychat, and the wasm-visor iframe browser.

## 1. Problem

The skynet **resolving proxy** "only works well for a direct route." Over a
**multihop** route the routing rules expire and the whole route has to be set up
again — a multi-second stall on every reconnect — so multihop skynet browsing is
effectively unusable through a browser-configured SOCKS5 proxy.

Separately, we now have **two-and-a-half implementations of the same thing** — dial
a PK over skynet with some routing policy, then carry bytes:

- **skysocks-client-lite** (`cmd/wasm-visor/skysocks_js.go`) — clearnet egress.
- **the resolving proxy** (native `pkg/skynetweb` + wasm `fetchDmsg`) — skynet/dmsg sites.
- **skychat** (`skynet:1` networker) — messages, which also pick a route.

None of them expose any routing control, and only one of them (skysocks-lite)
actually keeps its route alive. This RFC unifies the three and fixes the
route-death with a single primitive.

## 2. Root cause of route-death (measured in code)

- A routing rule's TTL is **10 min** — `DefaultRouteKeepAlive` (`pkg/router/router.go:34`),
  checked by `ruleIsTimedOut` (`pkg/routing/table.go:261`), GC'd every 10 s
  (`DefaultRulesGCInterval`, `router.go:36`; `router_gc.go`).
- Rules are refreshed **only by an open RouteGroup's own keepalive loop** — every
  5 min (`defaultRouteGroupKeepAliveInterval = DefaultRouteKeepAlive/2`,
  `route_group.go:27`), which re-arms **every intermediate hop** (`sendKeepAlive`,
  `route_group.go:1433`; `handleKeepAlivePacket` forwards it downstream,
  `router_packet.go:206`). An open-but-idle RouteGroup keeps all hops warm; a
  **closed** one stops refreshing and every hop's rule is reaped within ≤10 min.
- The native `.skynet` proxy dials a **fresh route per SOCKS5 connection with no
  caching** — `serveSOCKS5`'s Dial calls `DialSkynet` once per connection
  (`skynetweb/runtime.go:249`) → `router.DialRoutes` with a fresh ephemeral port
  (`embedded_skynetweb.go:273-274`) — and the RouteGroup closes when that
  connection ends. Browsers open/close SOCKS5 connections constantly, so every
  page load re-runs route-finding + rule-setup across all hops.

**Why direct "works" and multihop doesn't:** re-setup of a *direct* route is
instant (one transport, one hop); re-setup of a *multihop* route costs a
route-finder round-trip plus rule installation at every hop. Same code path, wildly
different cost. skysocks-lite doesn't exhibit this because it **caches and holds**
its route group per window (`skysocksSessions`, `skysocks_js.go:65`) and yamux-muxes
over it, so the keepalive loop keeps the multihop rules warm for the window's life.

## 3. What each surface does today

| Surface | Transport | Route reuse | Muxed? | Policy control |
|---|---|---|---|---|
| skysocks-lite (`skysocks_js.go`) | `DialRoutes`, `MuxRoutes=2` | **yes** — session cached per window | yes (yamux) | hardcoded |
| native resolving proxy `.skynet` (`embedded_skynetweb.go`) | `DialRoutes`, default opts | **no** — fresh per conn | no (1:1 forwarder) | none |
| native resolving proxy `.dmsg` | direct dmsg stream | n/a (long-lived session) | dmsg yamux | none |
| wasm resolving proxy (`fetchDmsg`) | **dmsg-direct only** (`FetchOverDmsg`) | n/a | dmsg | none |
| skychat `skynet:1` | `DialRoutes` via `NewSkywireNetworker` | per-conn (app framework) | app | none |

Two facts worth calling out:

- The **skynet forwarding server is a raw 1:1 port-forwarder** — `PerformHandshake`
  dials a port and pipes (`skynetweb/runtime.go:58-63`); there is no `Accept`
  loop, so a route can carry exactly one forwarded connection. This is *why* a
  cache-map alone can't fix it: you can't reuse a 1:1 conn for a second browser
  request. Reuse requires **muxing**.
- The **wasm iframe resolving proxy is dmsg-direct for everything**, including
  `.skynet` — so it has no route-death problem *and* no real skynet multihop
  routing. The route-death is a native-external-browser problem; giving the iframe
  true skynet routing is a separate enhancement (§6).

## 4. The fix: a held, muxed route group with idle-TTL (one fix for both cases)

The operator's instinct was to hold the route open "while the page is open," and to
worry that an external browser can't signal that. The better lever is that **the
route lives on the visor, not the browser** — so no browser-side signal is needed.

Generalize the skysocks-lite pattern into a shared primitive:

> **`skyroute.Pool`** — keyed by `destPK` (+ remote port). `Get(destPK)` returns a
> **held, yamux-muxed RouteGroup**, dialing + caching it on first use and reusing
> it afterwards. Each caller opens a **yamux stream** per logical connection. The
> pool evicts a route group after an **idle TTL** (no open streams for N seconds,
> default 2–5 min) and closes it — until then its keepalive keeps every multihop
> hop warm, so the *next* request reuses a warm route with **zero setup**.

This requires making the **skynet forwarding server yamux-aware**: one route group
= one yamux session; each accepted stream runs the existing `PerformHandshake` +
forward. (Version-negotiated / new forwarding port so old peers still work 1:1.)

Consequences:

- **External-browser SOCKS5** benefit with no page-open signal — the visor holds
  the warm route across the browser's connection churn; idle-TTL reclaims it.
- **iframe browser** benefits identically; window-close becomes an *optional eager
  release* (free the route now instead of waiting for the idle TTL) — a
  nice-to-have, not the mechanism.
- **skysocks-lite** collapses onto the same pool (it already muxes; just swap its
  private `skysocksSessions` map for `skyroute.Pool`).
- **skychat** `skynet:1` messages ride the same warm pooled routes.

## 5. Unified routing policy

Extract a `RoutingPolicy` (a `router.DialOptions` preset) that all three surfaces
consume, instead of each hardcoding:

```
type RoutingPolicy struct {
    MuxRoutes   int           // parallel routes (skysocks-lite uses 2)
    MinHops     int           // 0 = direct-when-available; >=2 = force multihop
    HoldTTL     time.Duration // idle route-group hold (pool eviction)
    Finder      string        // "latency" (default) | "hops" | ...
}
```

`skyroute.Pool` takes a `RoutingPolicy`; `DialSkynet`, skysocks-lite, and the
skychat networker all dial through it. One place to tune, one behavior everywhere.

## 6. Control surface

- **iframe ⚙ panel** (next to the proxy-log 🐞 toggle in `browse.js`): mux-route
  count, "force multihop / min hops," "hold route" duration, and a **live
  active-routes view** (hops + per-leg latency for the current dest). Drives both
  the skysocks-lite window and the resolving-proxy window through the shared
  policy. New JS hooks `skywireVisor.routePolicy(get/set)` + `skywireVisor.routes()`
  (all async — they now cross the Web Worker boundary; see the worker migration).
- **skychat**: a per-conversation "route" selector (dmsg-direct vs skynet, mux,
  min-hops) — the same policy object, so "control the exact route a message is sent
  over" falls out for free.
- **native mirror**: `cli` flags / config for the external-browser SOCKS5 proxy so
  it gets the same policy (hold-TTL, mux, min-hops).

## 7. Phases

1. **`skyroute.Pool` + yamux-aware forwarding server** (version-negotiated). Port
   the native `.skynet` proxy onto it → fixes route-death. *Verifiable with a real
   external browser on a multihop route.*
2. **Extract `RoutingPolicy`**; move skysocks-lite onto `skyroute.Pool` (dedup the
   two impls); route skychat `skynet:1` through it.
3. **Control surface**: iframe ⚙ routing panel + active-routes view; native cli
   mirror; optional real `DialRoutes` skynet path for the wasm iframe resolving
   proxy (so it can do multihop, gated by the same policy).

## Appendix A: skychat voice chat (separate track)

Three distinct projects, in ascending difficulty:

1. **wasm↔wasm over WebRTC — easy, best quality.** `getUserMedia` → media track on
   `RTCPeerConnection` (Opus, jitter buffer, AEC, PLC all built in), signaling over
   the existing WebRTC-over-dmsg channel. ~days. Caveat: `RTCPeerConnection` is not
   available in a Web Worker, so the voice PeerConnection must live on the main
   thread (or via the deferred main-thread PC-proxy the worker migration will want
   anyway). Does **not** ride skywire routes — orthogonal to this RFC.
2. **Voice over skywire routes — hard.** Reliable/ordered routes+dmsg cause
   head-of-line stalls; needs a **datagram transport** (faithful-UDP-over-dmsg,
   ~40% built & stalled, or QUIC datagrams) + an Opus codec in Go/wasm + a jitter
   buffer. Weeks; blocked on datagram transport.
3. **Native-visor voice — architectural friction.** No built-in audio pipeline;
   mic/Opus/playback via cgo fights the pure-Go/TinyGo constraint → belongs in a
   separate module (like `frank`), never in skywire's main `go.mod`.

Recommendation: if voice is wanted, do (1) as a self-contained wasm-visor feature.
