# dmsg server: all protocols on the one advertised ip:port

## Problem

dmsg servers gained extra carriers (WebSocket, WebTransport, QUIC) but each was
put on its **own port** with its **own** advertised address (`Server.AddressWS`,
`AddressWT`, `AddressUDP`). The discovery registration wasn't changed to match, so
in practice almost no server advertises WS — and a **browser wasm-visor can only
reach a server it has a WS address for**. Result: only one server in the fleet is
WS-reachable, the tab is stuck on a single seed session, and it can't reach the
discovery to register → route setup to it fails. (See
wasm-visor-discovery-registration.md.)

## Key insight: one ip:port already fits everything

TCP and UDP are **separate port namespaces**, and the protocols are
self-identifying on first bytes:

- **`TCP:port`** carries **raw-dmsg AND WebSocket**. A WS client opens with an
  HTTP/1 upgrade (ASCII); a native dmsg client opens with the Noise handshake
  (binary). A `cmux` demux routes `HTTP1Fast → ServeWS`, `Any → Serve`.
- **`UDP:port`** carries **QUIC AND WebTransport**. WT is HTTP/3-over-QUIC, so one
  QUIC listener with multiple ALPNs (`h3` for WT, the dmsg-quic ALPN for native)
  serves both. QUIC already binds the same port number as TCP.

So a server binds `TCP:30087` (raw + WS) and `UDP:30087` (QUIC + WT), advertises
the **one** existing `ip:port`, and every client reaches it over whatever protocol
it speaks. A browser just dials `ws://host:30087/dmsg`. **No new ports; the only
registration change is `AddressWS` pointing at the existing port** (which the
self-registration loop already does when the server serves WS).

## Implemented (this PR): the TCP half, proven

`dmsg.Server.ServeWithWS(lis, advertisedAddr, advertisedWSURL)`
(`pkg/dmsg/dmsg/serve_unified.go`): cmux-demuxes ONE listener into raw-dmsg + WS,
and sets `advertisedWSAddr` so the entry advertises `Address` + `AddressWS` on the
same port. Test (`serve_unified_test.go`): a raw-TCP client and a WS client both
connect to the same server on the same port and bridge a stream — green.

## Remaining wiring

1. **Server**: in `pkg/dmsg/dmsgserver/api.go` `ListenAndServe`, use `ServeWithWS`
   on the main TCP listener (pass the WS URL `ws://<public-host>:<main-port>/dmsg`)
   instead of `Serve` + a separate `WSAddress` listener in
   `pkg/services/dmsgsrv/dmsgsrv.go`. Add WT to the existing QUIC UDP listener
   (multi-ALPN) similarly. Then **redeploy the dmsg-server fleet** — after which
   every server is WS-reachable on its advertised port.
2. **Client (wasm-visor)**: derive the WS URL from the advertised `Server.Address`
   (`ws://<address>/dmsg`) — or just use the now-populated `AddressWS` — and seed
   from **all** discovered servers, not one. This gives the tab the multi-server
   connectivity it needs to reach the discovery and register (the route-setup
   prerequisite).

## How to tell which servers support WS today

Query the discovery entry: a WS-capable server's entry has an
`address (websocket): ws://…/dmsg` line (`skywire cli mdisc entry <pk>`). Today
only one does; after the fleet wiring above, all will (on their main port).
