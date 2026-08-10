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

`forwardViaPeer` / `PeerAnnounce` shipped opt-in
(`AnnounceAsPeer` / `AcceptPeerAnnouncements` default off). A relay capability
that fragments the network into "relays" and "non-relays" for no reason should
just be how a dmsg server behaves — so the on/off toggles are **removed** (not
merely defaulted on), with internal caps (max accepted peer links, max relayed
streams, the existing loop/hop guard) so an always-open relay can't become an
amplifier. "Implicitly enabled, bounded — not configurable off."

*(Shipped.)* Every server now announces itself as a forwardable peer over
its outbound links and honors inbound announcements unconditionally. The
`AnnounceAsPeer` / `AcceptPeerAnnouncements` config fields are gone; the
optional `accepted_peer_pks` allowlist remains (empty = accept any), joined by
`max_peer_links` (`DefaultMaxPeerLinks`) and `max_relayed_streams`
(`DefaultMaxRelayedStreams`) as the bounds. A bridge is counted against the
relay-stream cap only when one side is a peer session — plain local
client↔client bridging is never gated. Peer-originated frames are still refused
re-forwarding (`ss.isPeer` 1-hop guard).

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

### How CXO actually propagates — fan-in vs fan-out

CXO **does** relay hop-by-hop, but as an explicit **subscription tree**, not
epidemic gossip. A node forwards a feed only if it *itself* subscribed to that
feed, holds it, and serves it: `broadcastRoot` (pkg/cxo/node/feed.go) re-sends a
Root received on one connection to every *other* connection subscribed to that
same feed, and downstream nodes fill the objects from whoever advertised the
Root. Propagation follows the **subscription graph**, never the transport graph;
nothing discovers who wants a feed.

Because **each visor is its own publisher (feed PK = visor PK)**, the two
directions are not symmetric:

- **Upstream (visor → deployment): N distinct feeds — irreducible fan-in.** The
  TPD CXO aggregator subscribes once per visor feed (`conn.Subscribe(peerPK)`). A
  relay carrying upstream feeds must subscribe to *all* of them, so CXO relaying
  does **not** shrink the number of feeds — only the number of *connections*
  (many subscriptions multiplex over one conn to a high-degree public visor, the
  same way a dmsg server fans many clients into one session). The information
  still converges somewhere; the relays reduce connections and noise handshakes,
  not feed count.
- **Downstream (deployment → fleet): one aggregated feed — cheap fan-out.** The
  aggregator collapses N→1 and republishes a single feed; that rides a
  subscription tree of relay visors via `broadcastRoot` as-is. This is the only
  place the propagation win is free, and it is where a relay-visor tier genuinely
  replaces the dmsg-server star.

### Uptime through the relay tree: presence is not transitive

CXO's statefulness suggests uptime for free: connection lifetime = uptime, no
heartbeat. **That holds only on a direct visor↔collector edge.** CXO relaying is
store-and-forward — each hop terminates the upstream connection and re-originates
downstream — so a relay's knowledge that an origin went offline does **not**
propagate to nodes behind it.

This is measured, not assumed, by `TestRelayPresenceNotTransitive`
(pkg/cxo/treestore): with `V → R → A` (and a direct control `V → A2`), killing V
fires an `OnDisconnect` on both A2 (direct) and R (the relay's own edge)
immediately, but the leaf **A behind the relay receives nothing** — no
disconnect, no unsubscribe — for the full observation window. R keeps the feed
live (A is still subscribed, the last Root is cached), so it has no reason to
tear anything down. A can only infer V's absence from *silence* (no fresh
Roots), which is indistinguishable from "alive but idle."

**Consequence for the reward feed.** The moment the public-relay fan-in tier is
interposed — which is the whole point, to avoid N direct connections — passive
connection-lifetime uptime evaporates. The fix is to invert the problem from
*detecting absence* (which does not propagate) to *confirming presence* (which
does, as signed data):

- A visor **republishes its head Root periodically** even when nothing changed —
  a signed, timestamped liveness beat. A CXO `Root` carries `Seq`, `Time`
  (unix-nano) and `Sig` (pkg/cxo/skyobject/registry/root.go), so a republish is a
  fresh, distinct, **visor-signed** Root; unchanged child objects are
  content-addressed and dedup in the store (cxds `Set` bumps a refcount when the
  key already exists), so the beat costs ~one small Root on the wire, not a data
  resend.
- The collector reads the Root's timestamp + signature: relays **cannot forge or
  inflate** it (signed by the visor), and a visor that vanishes simply stops
  producing Roots and **times out** after K missed intervals. The republish
  interval is the uptime resolution knob. The project already republishes
  reactive feeds on an idle ticker for the "servable head Root" reason (SD's
  `servicesCXORepublishInterval`, the uptime publisher's per-window republish);
  this reuses that machinery as the liveness signal.

So the earlier apparent fork — direct-gateway vs. heartbeat — collapses: for a
**decentralized relay fan-in, uptime is signed periodic Roots**. Pure passive
connection-lifetime uptime survives only if the visor↔collector edge for the
uptime feed is kept **direct** (no relay), accepting fan-in at that one gateway.

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
