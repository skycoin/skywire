# Wasm-visor public autoconnect (WS/WT)

Status: design (native foundation first; wasm wiring deferred to the TinyGo port)

## Goal

A browser (wasm) visor should run **public autoconnect** the way a native visor
does — periodically establish transports to public visors — so that routes can
form through the mesh. Two wasm visors do **not** need a direct transport between
them: if each connects to public visors that share a route, the route-finder
stitches a path. WebRTC direct (browser↔browser) is a later optimization, not a
requirement.

## Transport choice: WS/WT, not WebRTC

"Public autoconnect" means *making the transports that are only possible because
the peer is public* — i.e. it listens for transports on a publicly reachable
`ip:port`. For a browser that's **WS / WT**:

- **WS** = TCP + one HTTP upgrade. **WT** = QUIC (0-RTT, multiplexed).
- Both dial directly to a known `ip:port`; no STUN/ICE/DTLS negotiation, no STUN
  dependency. Lighter than WebRTC.

**WebRTC is deliberately *not* the autoconnect transport.** It works for *any*
visor (NAT-traversal via STUN/ICE, signaling over dmsg) — native visors already
accept it by default (`pkg/visor/init_transport.go`: "WebRTC is on by default …
so browser visors can always reach it"). So WebRTC reach already exists and is a
*separate* capability (browser↔browser P2P). Using it for "public autoconnect"
would conflate the two and pay ICE overhead for no reason.

## IP resolution: self-report over dmsg (not the AR, not IP-in-SD)

A public visor's SD entry is `SWAddr` = **PK + port**, no IP. We deliberately do
**not** add the raw IP to the SD entry (privacy). Instead:

- The **port advertised in SD is a dmsg listener port**. An autoconnecting visor
  fetches HTTP-over-dmsg from `pk:port` and the visor replies with its **actual
  public `ip:port` for the master listening (WS) socket**. The port is already
  known from SD; only the IP is new, and it comes from the peer itself over dmsg.
- The address resolver (AR) would also answer this, but **autoconnect must not be
  *forced* to depend on the AR** — the dmsg self-report keeps it AR-independent.
  (The AR remains essential for NAT'd / holepunch visors, which autoconnect does
  not target.)

## TCP cmux (stcpr + WS) default-on

Today a native visor runs **no WS listener** unless `transport_port` is explicitly
set — the design-doc intent ("`stcpr_port` unset ⇒ ride the master port") was
implemented as opt-in (`if TransportPort != 0`). Fix:

- Enable the **TCP cmux (stcpr + WS) by default** on the stcpr TCP port (peek the
  first bytes: `GET … Upgrade: websocket` ⇒ WS, else the raw skywire handshake ⇒
  stcpr). This is **backward-compatible** (existing stcpr peers unaffected) and
  changes **no port** — so it never strands a router port-forward.
- Keep **UDP-side unification (sudph → stcpr port number) opt-in** (`transport_port`)
  — that's the part that shifts a UDP port and can break per-port forwarding.

Result: every public visor gets a WS listener on its existing stcpr TCP port
(random or fixed), with zero forwarding impact and no per-host port pinning.

## Plan / sequencing

The loop in `pkg/visor/autoconnect.go` is `*Visor`-coupled and hardcodes
SUDPH+STCPR. Steps:

1. **Extract** a parameterized core into `pkg/visor/visorcore` (transport-type
   list + public-visor source + `transport.Manager`); native wraps it with
   `[SUDPH, STCPR]` (behavior unchanged).
2. **TCP-cmux default-on** (native) — universal WS listener.
3. **dmsg self-address endpoint** (native) — serve the public `ip:port` over dmsg
   on the SD-advertised port.

   *These three are native-side and must deploy to public visors (auto-update to
   develop) before anything is observable.*

4. **Wasm wiring** (deferred until the TinyGo wasm-visor port is back in play):
   wasm runs the extracted loop with `[WS, WT]`, sourcing public visors + their
   `ip:port` from SD + the dmsg self-report. A net/http-free dmsg AR client is the
   AR-backed alternative for IP resolution and is also deferred to that phase.
