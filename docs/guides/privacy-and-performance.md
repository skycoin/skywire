# Tuning Skywire for privacy or performance

Skywire is built on cryptographic identity: every visor is a public key, and
every connection between visors is an encrypted transport to that key — there is
no plaintext traffic. On top of transports, the **routing layer** carries your
application traffic as source-routed, per-hop-forwarded packets. That routing
layer is where you choose *where on the privacy ↔ performance spectrum you want
to sit* — and it is almost entirely a matter of configuration.

This guide explains the levers and gives concrete recipes for three points on
that spectrum: **maximum performance** (the default), **balanced**, and
**maximum privacy**.

!!! note "Defaults favor performance"
    Out of the box a visor is tuned for throughput and low latency, and to be a
    useful, rewards-eligible member of the network. Everything below is opt-in
    hardening for operators who want to trade some performance for privacy.

## The three kinds of privacy

It helps to separate what you are actually trying to protect:

| Dimension | The concern | Where it's addressed |
|---|---|---|
| **Data privacy** | Can anyone read my traffic? | Always encrypted (Noise, post-quantum-hardened); some transport types are double-encrypted. Nothing to configure. |
| **IP privacy** | Which keys learn my machine's IP address? | Which visors you make transports to / accept transports from, and whether you register in the address resolver. |
| **Metadata privacy** | Can an observer correlate *who is talking to whom / traffic patterns*? | Hop count, multiplexed & rotating routes, exit-node choice. |

Most operators only care about **performance**. IP and metadata privacy are
niche but real use cases, and Skywire supports pushing them hard.

## Data privacy — already handled

Every transport carries a Noise-XK session (mutually authenticated by public
key) with an **ML-KEM post-quantum hybrid** handshake. Several transport types
are additionally **double-encrypted** — the Noise session rides on top of an
already-encrypted transport (QUIC/TLS bound to the static key, WebTransport, or
WebRTC/DTLS):

| Encryption | Transport types |
|---|---|
| Single (Noise over plaintext base) | `stcpr`, `sudph`, `stcp`, `swsr` (native ws) |
| Double (Noise + transport TLS/DTLS) | `squicr`, `swtr` (WebTransport), `webrtc`, `swsr` from a browser (wss) |
| Relay (end-to-end Noise via a dmsg server) | `dmsg` |

You do not configure this — it is on for all traffic. You *can* prefer
double-encrypted carriers with `transport_preference` (below).

## The levers

All of these live in the visor config (`skywire.json`). Knobs that also have a
`config gen` / `autoconfig` flag are noted; the rest are set by editing the
config JSON (or `skywire cli config gen` and hand-editing).

### Being reachable — `public_autoconnect`, `--public`, `ar_transport_limit`

- **`--public`** registers the visor in the public-visor service discovery so
  others autoconnect *to* it. A public visor advertises reachability.
- **`public_autoconnect`** (default `true`) runs the periodic loop that builds
  transports to public visors — the mesh backbone. Disabling it (`config gen
  --disable-public-autoconn`) makes the visor connect only to peers *you* set up.
- **`ar_transport_limit`** controls the **address-resolver (AR) entry** — the
  record that lets an arbitrary visor resolve your PK to an address and dial you:
    - `0` (default): stay registered — reachable inbound.
    - `N > 0`: deregister after N transports exist.
    - `N < 0`: **never register with the AR at all** — no one can look up your
      address, so no one can initiate a transport to you (and thus learn your IP)
      unless *you* dialed them first.

!!! warning "Rewards"
    A visor with public autoconnect disabled is **not rewards-eligible**. Max-privacy
    setups are deliberately outside the rewards model.

### What transports the visor will create — `no_direct_transports`, `transport_create_deny`, `persistent_transports`

- **`persistent_transports`** — a list of `{pk, type}` the visor always maintains
  (recreated across restarts). This is how you say *"only ever connect to these
  specific, trusted visors."*
- **`no_direct_transports`** (bool) — never create a **direct peer-to-peer**
  transport (`stcpr`/`sudph`/`squicr`/`swsr`/`swtr`/`webrtc`); dmsg (a relay, not
  direct p2p) is still allowed. A direct transport reveals your IP to the peer;
  disabling direct creation means you reach others only over the mesh / dmsg.
- **`transport_create_deny`** — advanced per-type deny list (may include `dmsg`),
  for forcing a pure-direct or fully-offline posture (mainly for testing).

### How routes are built — `min_hops`, multiplexing, rotation

- **`min_hops`** (visor-wide): `0` disables routing; `1` allows a direct 1-hop
  route when a transport exists; `≥ 2` **forces every route through at least N
  intermediate visors**, so no single hop sees both ends.
- **Multiplexed routes** (`--mux N` / `--routes N` per app): split a flow across
  N disjoint routes. No single route carries the whole conversation, which is
  both a **throughput** win and a **metadata** win. This is the single best
  "both worlds" setting.
- **Route rotation** (via routing policy): periodically drop and re-establish
  muxed legs across the eligible-peer set, so even the *set* of intermediates a
  flow uses changes over time — strong metadata protection.
- **Asymmetric forward/reverse** (`--forward-*` / `--reverse-*`, per-dial): the
  forward and reverse directions can use *different* routes — e.g. a single
  direct forward leg for the request with a 4-way multiplexed multihop reverse
  for a bulk download. Supported per-dial; not applied automatically (see
  [notes](#a-note-on-asymmetric-routing)).

### Which carriers to prefer — `transport_preference`

Order the transport types the route-finder and dialer prefer, e.g. to favor
double-encrypted `squicr`/`swtr`, or to avoid a family your network blocks.

## Recipes

### Maximum performance (default)

Nothing to do. Public autoconnect on, `min_hops` 1 (direct where possible),
single routes, all carrier families available. For heavy flows, add
multiplexing per app:

```
skywire cli vpn start --srv <server-pk> --mux 4          # 4 parallel routes
skywire cli proxy start --srv <server-pk> --routes 4
```

Multiplexing gives you more throughput *and* better metadata privacy at once —
recommended for most people who want a little of both.

### Balanced

Keep autoconnect/rewards, but push metadata privacy with multihop multiplexed,
rotating routes for sensitive apps:

```
skywire cli proxy start --srv <exit-pk> --routes 4 --min-hops 2
```

Add a routing policy with rotation for periodic leg turnover (see the
[routing-policy RFC](../routing_policy_rfc.md)).

### Maximum privacy

The goal: **no one learns your IP, and no observer can correlate your traffic.**
Combine the levers:

1. **Only connect to trusted visors.** Pin them in `persistent_transports` and
   set `no_direct_transports: true` so the visor never dials a direct p2p
   transport to anyone else. The only visors that ever learn your IP are the
   trusted ones you chose.
2. **Be invisible inbound.** Do **not** run `--public`, and set
   `ar_transport_limit: -1` so you never register in the address resolver — no
   one can resolve your PK to an address, so no one can initiate a transport to
   you and learn your IP.
3. **Reach deployment services through a non-public dmsg server.** Connect to a
   dmsg server that itself is not publicly listed and that relays to the rest of
   the deployment's dmsg servers, so those servers see the relay's IP, not yours.
   *(Advanced/emerging — see [status](#feature-status) below.)*
4. **Route with depth + spread + rotation.** For any app traffic, use
   `--min-hops 4` (or 5) with `--routes 4` and a rotating routing policy, so the
   full traffic is never on one path and the path set changes over time —
   defeating traffic correlation.
5. **Rotate exits.** When using a skysocks exit for clearnet, change the exit
   node intermittently.

!!! danger "This is deliberately off the beaten path"
    A max-privacy visor is not public, not rewards-eligible, and slower. It only
    works if the trusted peers you pin are actually up and reachable. Verify your
    connectivity (`skywire cli tp ls`, `skywire cli route ls`) after locking it
    down.

### Beyond the visor: anonymizing the dmsg client itself

The strongest posture treats even the visor's own dmsg control connection as
something to anonymize. A dmsg client can be pointed at a dmsg server *through a
visor's skysocks-client* — so the dmsg server sees the skysocks exit's IP, not
the client's. With a sufficiently deep, multiplexed, rotating route to the exit
(4–5 hops), traffic correlation back to the origin is not feasible, and the exit
can be rotated intermittently. This is the logical end state of the recipe above
and is an active area of work.

## A note on asymmetric routing

Skywire's route groups already support **different forward and reverse routes**
(`ForwardMinHops`/`ReverseMinHops`, `ForwardMuxRoutes`/`ReverseMuxRoutes`), and
the data plane handles the two directions having different leg counts. This is
exposed per-dial today but is **not chosen automatically** — a dial uses the same
shape both ways unless you ask otherwise. Whether the visor *should*
automatically pick an asymmetric shape (e.g. a light direct forward leg for
requests + a fat multiplexed reverse for downloads) is an open design question;
for now it is a manual/policy tuning lever.

## Feature status

Most levers here are established and shipping:

- ✅ `public_autoconnect` / `--public`, `ar_transport_limit`, `min_hops`,
  `persistent_transports`, `transport_preference`, per-app `--mux`/`--routes`/
  `--min-hops`, `no_direct_transports` / `transport_create_deny`, routing policy
  with rotation.
- 🧪 **Non-public dmsg server** (relay-only, unregistered) and **dmsg-client-over-skysocks**
  egress are emerging / partially implemented — treat as advanced and verify
  behavior before relying on them for a threat model.

!!! info "Config vs. flags"
    The privacy knobs (`ar_transport_limit`, `min_hops`, `no_direct_transports`,
    `transport_create_deny`, `transport_preference`, `persistent_transports`) are
    set today by editing `skywire.json`. `config gen` / `autoconfig` flags for
    them are being added so the install-command generator and `skywire autoconfig`
    can set them directly.

## See also

- [manual-routing.md](manual-routing.md) — building routes by hand
- [routing_policy_rfc.md](../routing_policy_rfc.md) — the per-dial routing-policy DSL (rotation, min-hops, distribution)
- [socks5.md](socks5.md) / [vpn.md](vpn.md) — the client apps whose flags carry the per-dial tuning
