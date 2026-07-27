# RFC: SkyDEX as a website over dmsg

**Status:** Draft / discussion. Not a committed direction.

**Question this answers:** is there anything about SkyDEX that requires the
dedicated `skydex-client` app + custom market protocol, or could the exchange
just be a website served over dmsg (like every other skywire deployment
service, reachable through the resolving proxy)?

Short answer: it *could* be a website over dmsg, and the pieces skywire already
has make it tractable — but two properties (**per-trader cryptographic
identity** and **client-side order signing**) are the real design forces, and
the current split is largely **inherited from skycoin's upstream skydex**, not a
skywire-first decision.

## 1. What SkyDEX is today

Both skywire apps are thin wrappers around the skycoin `skydex` engine
(`github.com/skycoin/skycoin/{cmd/skydex-*,src/skydex}`); the wrappers only
supply the transport.

**skydex-market** (`cmd/apps/skydex-market`):

- `appCl.Listen(appnet.TypeDmsg, port)` — listens on a dmsg routing port
  (default `8050`).
- Derives each trader's identity straight from the transport:
  ```go
  identify := func(conn net.Conn) (string, error) {
      raddr, ok := conn.RemoteAddr().(appnet.Addr) // dmsg-authenticated
      ...
      return raddr.PubKey.Hex(), nil               // never trusted from payload
  }
  ```
- Hands the listener + `identify` to the skycoin engine, which owns the SQLite
  store, order matching, and escrow. It also serves an **operator** web UI
  locally on `:8050`.

**skydex-client** (`cmd/apps/skydex-client`):

- Serves the **trading UI** (a SPA from the skycoin engine) on localhost `:8051`.
- On demand, dials the market's PK over dmsg and wraps the stream in
  `skymarket.Conn` — a **custom, stateful, framed protocol** (not HTTP).
- Runs locally so the user's keys and order signing stay client-side; it holds
  no persistent connection while idle.

## 2. Why it isn't "just a website" today

Three forces, in priority order:

1. **Per-trader cryptographic identity.** The market authenticates each trader
   by the dmsg public key on the *accepted connection*. That is what an
   exchange fundamentally needs: whose order is this, whose escrow balance, who
   signed this fill. A plain website reached through the **shared** resolving
   proxy would arrive at the market as the *proxy's* PK — every trader would
   collapse to one identity.
2. **Client-side key custody / signing.** A non-custodial DEX wants orders
   signed with the trader's own keys. A local app (UI on localhost, keys local)
   keeps signing client-side; a remote hosted site would have to be trusted with
   keys or bridge to a wallet.
3. **Live order-book stream.** Matching wants a persistent bidirectional feed
   (book pushes out, orders in). Request/response HTTP is a poor fit — though
   WebSocket/SSE over dmsg would cover it, so this is a "cleaner fit" argument,
   not a hard blocker.

And the pragmatic reason: the skywire wrappers are intentionally thin over
skycoin's existing client/market split and its `skymarket.Conn` protocol.

## 3. Proposed website-over-dmsg design

The exchange becomes a website over dmsg if we preserve identity and signing:

- **Market serves HTTP + WebSocket over dmsg** instead of the framed protocol
  — either an `http.Server` on the appnet listener, or via the existing
  `dmsghttp` machinery. The order-book feed rides a WebSocket; order placement
  is a POST.
- **Identity is preserved by reaching it through the user's *own* visor
  gateway.** Every skywire user already runs a visor; its
  `dmsgweb` / resolving-proxy dials with *that user's* PK. So the market still
  sees a distinct, transport-authenticated PK per trader — the same
  `RemoteAddr().(appnet.Addr).PubKey` it uses today — as long as the PK is
  injected by the **market's own dmsg-HTTP listener** (from the authenticated
  transport) and surfaced to the HTTP handler as a trusted, non-spoofable value.
  It must never be read from a client-supplied header.
- **The trading UI becomes a static SPA** served by the market over dmsg (or
  embedded / hosted anywhere). The dedicated `skydex-client` app is no longer
  required — a browser pointed at the market's `.dmsg` address is the client.
- **Order signing uses the existing skycoin-web wallet-over-mesh** integration,
  so the browser signs with the user's keys client-side and the site stays
  non-custodial.

See [resolving proxy](guides/resolving-proxy.md) and
[dmsg LAN gateway](guides/dmsg-lan-gateway.md) for the per-user gateway model,
and the skycoin-web wallet work for mesh-side signing.

## 4. The identity subtlety (most important)

The whole design hinges on this: **the trader PK must come from the
dmsg-authenticated transport, set by the market's own listener — never from the
HTTP payload or a header a client can forge.** Today's `identify` closure gets
this for free from `appnet.Addr`. An HTTP-over-dmsg market must do the
equivalent: the dmsg-HTTP server stamps the connection's authenticated PK into
the request context, and the engine reads only that. A shared public proxy in
front of the market breaks this (collapses identity) — so the supported access
path is each user's *own* gateway, which is the normal skywire posture anyway.

## 5. Trade-offs / open questions

- **Upstream cost.** The skycoin skydex engine is protocol-oriented
  (`skymarket.Conn`). A website model needs either an HTTP/WS adapter in the
  skywire wrapper or an upstream engine change. The thin-wrapper property is
  the main thing we'd give up.
- **Signing UX.** Wiring wallet-over-mesh signing into the trading SPA is real
  work and needs a careful non-custodial escrow story.
- **Discovery.** The market should register a resolver name so users reach it
  by name, not a raw PK (see resolver aliases).
- **Transition.** Both can coexist: keep the protocol app for existing clients
  while standing up an HTTP/WS dmsg endpoint, and retire the dedicated client
  once the website reaches parity.

## 6. Recommendation

Treat "SkyDEX as a website over dmsg" as viable and desirable long-term — it
removes a bespoke app + protocol and folds the DEX into the same
website-over-dmsg + wallet-over-mesh model as the rest of the platform. The
gating work is (a) an HTTP/WebSocket front on the market that stamps the
transport-authenticated trader PK into each request, and (b) in-browser order
signing via wallet-over-mesh. Until then the dedicated `skydex-client` remains
the pragmatic path because it inherits skycoin's engine wholesale.
