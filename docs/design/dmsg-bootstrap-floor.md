# RFC: dmsg as a bootstrap-only floor — visor-relayed dmsg + deployment services off dmsg-server dependence

Status: draft / design. Tracks the multi-part transition that lets a visor stop
depending on persistent connections to public dmsg **servers** once it has its
own transports, using the transport graph itself as the relay fabric.

## Motivation

dmsg is three things at once: a discovery layer, a control/bootstrap layer (route
setup, transport & service discovery all ride it), and a client↔server↔client
**relay** network. Because every visor keeps sessions to public dmsg **servers**,
the network pays for that fan-out continuously. Two concrete symptoms:

- **Session accumulation.** A client's initial connect caps at `sessions_count`,
  but the dial path opens on-demand rendezvous sessions to reach peers delegated
  to servers it wasn't connected to, and the ping loop then keeps them alive — so
  the count drifts up toward "all servers." (Fixed by the idle-session reaper,
  below.)
- **Server dependence.** Even with plenty of transports, a visor must stay
  attached to dmsg servers because that is how it reaches other visors and the
  deployment services.

The long-standing intent is that **dmsg servers are a bootstrapping layer**: a
fresh visor uses them to obtain its first transports and routes, after which it
should be able to reach the mesh through its own transports and relay for others,
needing dmsg servers only as a fallback.

Two premises make this a *bounded* change rather than a rewrite:

1. **dmsg already relays.** A dmsg server forwards between clients on *different*
   servers via `forwardViaPeer` / `PeerAnnounce` — it is not a pure star. And it
   needs no routing to do so: the destination **PK is the address**; the server
   resolves it with one map lookup (`serverSession(DstAddr.PK)`) and byte-splices
   the two streams (`bridgeStream`). Star topology + map lookup *replaces*
   routing.
2. **A visor already runs a dmsg-server-style stream mux over a point-to-point
   transport with no router route** — `VStreamMux` (route ID 0, the "direct
   skynet" path). It terminates streams locally today; it does not yet forward.

## Components

### 1. Server↔server relay, always on

`forwardViaPeer` / `PeerAnnounce` exist but ship opt-in
(`AnnounceAsPeer` / `AcceptPeerAnnouncements` default off). A relay capability
that fragments the network into "relays" and "non-relays" for no reason should
just be how a dmsg server behaves — so the on/off toggles are **removed** (not
merely defaulted on), with internal caps (max accepted peer links, max relayed
streams, the existing loop/hop guard) so an always-open relay can't become an
amplifier. "Implicitly enabled, bounded — not configurable off."

### 2. Visor as a dmsg relay (the `VStreamMux` bridge)

Generalize the server's relay to any visor, carried over its skynet transports.
This is `VStreamMux` + a `bridgeStream` analogue. Five concrete gaps:

1. **Forward-to-third-party branch** — `HandlePacket` only terminates locally;
   add a branch that opens a matching `VStream` on an outbound transport and
   splices, mirroring the dmsg server. *(largest gap)*
2. **Destination PK in the frame** — today the frame carries only `senderPK`; a
   relay can't tell who a stream is for. Add a dst field (dmsg's
   `StreamRequest.DstAddr`).
3. **Peer discovery for the broker** — which peers can I relay to? The transport
   graph (TPD) is most of the answer.
4. **Loop / hop guard** — refuse to re-forward peer-originated frames, as dmsg
   does.
5. **Stream-id remap** — a fresh mux stream per bridged stream, as dmsg's
   `forwardRequest` does.

Because the relay is PK-addressed forwarding (not a source-routed skynet path),
reaching a relay you already have a transport to needs **no route**. This is the
transition's real prize: the fleet connects to a few relay visors over existing
transports; only those relays keep dmsg-server sessions; fan-out collapses.

### 3. Idle-session reaper

Close sessions that are streamless and beyond `sessions_count`, so on-demand
rendezvous sessions no longer accumulate. Disabled at `sessions_count == 0`
(connect-to-all/dev). *(Shipped — the first PR of this effort.)*

## The hard part: deployment services off dmsg-server dependence

The reaper and the relay reduce dmsg-**server** fan-out without touching the
deployment services — a visor still reaches TPD/AR/RF/SD/UT over dmsg, now
*through a relay* rather than a direct server session. But to make dmsg truly
**bootstrap-only**, the deployment services themselves must be reachable off the
dmsg-server layer. That is the knot.

### Why it's a knot

- The deployment services are **dmsg-only**. `getHTTPClient` actively rejects
  non-dmsg schemes ("plain HTTP to deployment services is no longer supported").
- Routes are set up **via** RF/TPD/AR — the very services. So route-based service
  access can't fully *replace* dmsg: dmsg wins by being an always-on lower layer
  that needs no route.

### Proposed approach: forward the service's CXO interface over skynet

Do **not** proxy the services' HTTP over the mesh — HTTP is stateless, so hop-by-
hop proxying is awkward and per-request. Instead lean on the fact that the
deployment feeds are already **CXO**, a persistent subscription/broadcast
channel:

1. Each deployment service exposes a **local CXO listener on loopback TCP**
   (localhost only — never proxied to clearnet).
2. A **co-located visor** on the same host **forwards that local TCP interface
   over skynet** (the same shape as skyforwarding). The service stays a plain
   process; the forwarding visor bootstraps over dmsg like any other visor.
3. Clients reach the service's CXO over skynet via a **`<PK>:<port>` route-dialing
   RoundTripper** — a client shaped like `dmsghttp`'s transport, but which
   route-dials the forwarding visor instead of dialing a dmsg session. This slots
   in behind a **new scheme branch in `getHTTPClient`** (the point that currently
   rejects everything non-dmsg).

This gives CXO the long-lived stateful pipe it wants, and — critically — **avoids
the bootstrap paradox**: nothing about the deployment host becomes circular,
because the forwarding visor still bootstraps over the dmsg-server floor. dmsg
stays the bootstrap/fallback layer; steady-state traffic moves onto skynet.

### Rejected alternative: deployment-services-as-visors

Making the deployment services *be* visors (or having a visor run them) is more
invasive and re-enters the paradox — a service that needs the deployment layer to
bootstrap can't *be* the deployment layer without a floor beneath it. Keeping the
services as plain processes with a co-located forwarding visor is strictly
simpler.

### Bootstrap sequence (steady state)

1. A fresh visor connects to the dmsg-server floor (as today) and obtains its
   first transports + routes.
2. It reaches deployment services over the CXO-forward (via skynet), and other
   visors via relay visors over its transports.
3. The idle-session reaper lets its dmsg-**server** sessions settle toward
   `sessions_count`; once it has transports and relays, it need not hold more.
4. dmsg servers remain only as the bootstrap/fallback floor.

## Open questions

- **Relay discovery / rendezvous.** With visors as relays, how does a visor pick
  a relay that a target is also reachable through? Single-hop rendezvous (both
  ends share a relay, like dmsg today) vs. multi-hop overlay (needs path
  discovery). The transport graph (TPD) is the substrate; the selection policy is
  unspecified.
- **Relay reachability & load.** A relay must be inbound-reachable (public
  stcpr/sudph endpoint or a maintained link) and bears bandwidth for others —
  which visors relay, and how is their load bounded?
- **Trust / metadata.** A relay sees stream metadata (content stays Noise-
  encrypted). Server relays are semi-trusted; arbitrary visor relays less so.
- **CXO-forward specifics.** Exact framing of the loopback CXO listener, the
  `<PK>:<port>` RoundTripper's route-dial + reconnect, and how a client discovers
  which forwarding-visor PK fronts a given service.

## Related

- `pkg/dmsg/dmsg/` — `server_session.go` (`forwardViaPeer`, `bridgeStream`),
  `server.go` (`ServerConfig`, `PeerAnnounce`), `client.go` (reaper).
- `pkg/transport/vstream.go` — `VStreamMux`.
- `pkg/visor/init.go` — `getHTTPClient` (the dmsg-only gate).
- `docs/design/dmsg-forward-proxy.md`, `docs/design/dmsg-server-protocol-unification.md`.
