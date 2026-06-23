# Transport port unification + AR-integrated discovery

Status: design (incremental implementation in progress)

## Problem

A visor today binds a **separate listening port per transport type** — `stcpr_port`
(TCP), `sudph_port` (UDP), `quic_port` (UDP) — plus the newer WS/WT/WebRTC types,
each with their own socket. Each type also discovers peers differently:

- **stcpr / sudph / quic** register their bound address in the **address resolver
  (AR)** and resolve peers from it.
- **stcp** is the *static* variant of stcpr: a manual `PK → addr` table in config,
  no AR.
- **WS / WT** were added with their own manual `PK → URL[+cert-hash]` tables
  (`WSTable` / `WTTable`), and are **not** AR-integrated.
- **WebRTC** signals by PK over dmsg (no endpoint to register or resolve).

Two consequences:

1. **N firewall rules / N AR registrations / N config knobs** for one visor.
2. **WS/WT aren't autoconnect-able** — a peer can't *discover* another's WS/WT
   endpoint (manual table only), whereas the AR-resolved types can.

## Goals

- **One listening port** by default — `transport_port` (one `tcp` + one `udp`
  socket on the same number), with every type demultiplexed onto it.
- **AR-integrated discovery for WS/WT**, like the other resolved types, so they
  become autoconnect-able (a browser tab resolves a peer's WS endpoint from the AR
  and dials it — it can't register one, having no listener, but it can resolve).
- **A static `PK → endpoint` table for every type that makes sense** (the STCP
  model generalized), for manual / no-AR links.
- **Reverse-compatible** with the existing per-type port config.

## Model

### Listening

- `transport_port` (master, default `0` = random): one TCP + one UDP listener,
  demuxed by protocol signature. Carries every type that does not have its own
  port.
- Per-type ports (`stcpr_port` / `sudph_port` / `quic_port` / …, default `0` =
  *ride the master port*; explicit = *break out onto a dedicated port for that
  type only*).

So `0` no longer means "own random port" — it means "use the master port" (itself
random if `0`). Setting a specific port pulls that one type out onto its own
socket. This is a standard global-default + per-item-override pattern.

**Disambiguation** is bookkeeping in the transport manager: it knows which
listener (master vs a dedicated one) serves which type; each listener registers
*whatever port it actually bound* with the AR; peers resolve from the AR. A demux
only ever classifies the types assigned to its socket.

### Demultiplexing

- **TCP (`transport_port`/tcp)** → **stcpr + WS**: peek the first bytes on accept.
  `GET … HTTP/1.1\r\n … Upgrade: websocket` → WS; otherwise the raw skywire
  handshake → stcpr. (The `cmux` technique.)
- **UDP (`transport_port`/udp)** → **quic + WT + sudph + webrtc**: classify each
  datagram on one socket —
  - QUIC long-header (high bit + fixed bit set) → quic-go; **quic vs WT split by
    ALPN** inside the handshake (skywire-quic vs `h3`).
  - STUN magic cookie `0x2112A442` / DTLS content-type → WebRTC (pion
    `SettingEngine.SetICEUDPMux` shares one socket).
  - otherwise → sudph (KCP).

  ICE already demultiplexes STUN/DTLS/data on a single socket, and quic-go/pion
  both accept an externally-provided `net.PacketConn`, so this is proven ground.
  The implementation is a `udpDemux` wrapping one `net.PacketConn` that routes
  inbound packets by signature to per-protocol virtual `PacketConn`s.

### Discovery

Each AR-resolved type registers its bound endpoint with the AR and resolves peers
from it. WS/WT extend the AR record with a URL (+ WT cert-hash) rather than a bare
`IP:port`. In addition, each type optionally carries a **static `PK → endpoint`
table** in config (the STCP model) for manual / no-AR links — stcpr/stcp (addr),
quic/sudph (`IP:port`, direct dial, no hole-punch), WS/WT (URL). WebRTC's "manual"
mode is the by-PK `dialTransport` trigger, since signaling rides dmsg.

## Reverse compatibility

- **Behaviorally compatible:** an existing `stcpr_port: 0` binds stcpr on its own
  random TCP port today; under this model it binds on the master random TCP port
  (shared with WS). stcpr still works and registers its bound port in the AR — the
  *outcome* is identical, just the socket is shared.
- **Explicit per-type ports** are still honored as dedicated breakouts, so
  hand-tuned firewall configs keep working.

## Incremental plan

1. **QUIC default-on** — drop the `quic_port == 0` opt-in gate; bind ephemeral
   when unset, like sudph. (done)
2. **Static `PK → endpoint` tables** for the AR-resolved types that lack one
   (quic/sudph), mirroring STCP. (additive, low-risk)
3. **One master port — UDP first**: `udpDemux` shared socket for quic + sudph +
   webrtc (+ WT). Then **TCP**: peek-router for stcpr + WS.
4. **AR-integrate WS/WT**: register/resolve WS/WT endpoints in the AR (extends the
   AR record with a URL + WT cert-hash; may require an AR-service change — to be
   confirmed).
5. **Config collapse**: `transport_port` master + per-type overrides; keep the old
   keys as overrides for compatibility. (done — a non-zero per-type port breaks
   that type out onto its own socket; 0 rides the master.)

Each step is independently shippable.

## Status

- **Done:** QUIC default-on; UDP demux (quic+sudph) over one socket; TCP demux
  (stcpr+WS, cmux) over one socket; `transport_port` opt-in wiring; per-type-port
  override (break-out). A visor with `transport_port: N` listens for every type on
  `N/tcp` + `N/udp`. Each carrier validated over its demux with real traffic.
- **Remaining:** AR-integrate WS/WT for discovery (needs an address-resolver
  service change — new bind/resolve for ws/wt + WT cert-hash storage); webrtc over
  the shared UDP socket (optional — pion `SetICEUDPMux`); a static `PK → endpoint`
  table for quic/sudph (the STCP model generalized).
