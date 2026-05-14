# Cascading Route Setup

## Overview

Cascading Route Setup eliminates the Route Setup Node's (RSN) dependency on DMSG for installing routing rules on intermediate hops. Instead of the RSN independently dialing every hop via DMSG, it constructs a nested message that cascades along the route's own pre-existing transports using the route ID 0 control channel.

This specification also covers:
- RSN standalone visor configuration with transport manager
- Transport label "setup" for RSN transports

## Motivation

Currently, route setup requires DMSG connectivity between the RSN and every visor in the route. If DMSG is unavailable, no routes can be established — the entire network depends on DMSG servers being online. DMSG was designed as a bootstrapping layer, not a permanent dependency.

The cascade protocol moves route setup to the transport layer:
- The RSN is reachable via transport-level relay (route ID 0)
- Routing rules are installed hop-by-hop over the route's own transports
- DMSG becomes a fallback, not a hard requirement

## Design Principles

1. **The RSN never participates in data routes.** It is an orchestrator, never an intermediary hop. Its transports carry only control-plane traffic.
2. **The RSN provides authorization.** Only a trusted RSN can install routing rules. Each hop verifies the RSN's signature.
3. **The RSN provides privacy.** Intermediate hops see the RSN's signature, not the requesting visor's identity. Intermediary forwarding rules contain only route IDs and transport IDs — not source or destination PKs.
4. **Each hop knows only its neighbors.** An intermediate hop sees only the previous and next hop (from its rule and the transport it relays on). It cannot inspect the nested payload destined for later hops.
5. **Transports in the route already exist.** Route setup is requested only after the path is calculated and all transports along it are established.

## Architecture

### RSN as a Standalone Visor

The RSN is its own visor with:
- Its own PK/SK identity
- A transport manager (initiates STCPR/SUDPH transports outward)
- Autoconnect enabled (to build its transport connectivity)
- DMSG connectivity (as any visor, for bootstrapping and fallback)
- No hosted applications
- No registration in the address resolver (does not accept inbound transports — only initiates outward)
- No participation as an intermediary hop in any data route

### Transport Label "setup"

The RSN creates all its transports with the label `"setup"`. The responder adopts this label via the settlement handshake (existing label propagation mechanism). Both ends of an RSN transport carry the "setup" label.

Since visors periodically re-register their transports with transport discovery (TD), RSN transports will appear in TD with this label. The route finder and local route calculation **exclude transports with label "setup"** from data route path calculations. This is simpler and more reliable than matching against RSN public keys.

The "setup" label means: this transport exists for control-plane communication with the RSN. It is visible in TD (the mesh is continuous), but it is never used as a hop in a data route.

### Transport Capacity Management

The RSN initiates transports to other visors via autoconnect. It does NOT register with the address resolver, so it never receives unsolicited inbound transports. The RSN controls its own connectivity by choosing which visors to transport.

A configurable `max_transports` limits how many transports the RSN maintains. The autoconnect logic stops initiating new transports once the limit is reached.

## Packet Types

New packet types are added to the transport frame format, all using route ID 0:

| Type | Value | Description |
|------|-------|-------------|
| `CascadeSetupPacket` | `10` | Carries a cascade setup message (reserve or install phase) |
| `CascadeAckPacket` | `11` | Carries a cascade acknowledgment/confirmation |

Both use route ID 0 and are intercepted at the transport layer before reaching the router. Old visors that don't recognize these types drop them silently (existing behavior for unknown packet types on route ID 0).

### Capability Negotiation

A new capability flag is added to the transport handshake:

```
CapCascade = 1 << 2
```

Visors that support the cascade protocol advertise this during the transport handshake. The RSN checks this flag to determine which hops support cascade.

## Cascade Message Format

### CascadeSetup

```
CascadeSetup {
    Phase       uint8           // 0 = Reserve, 1 = Install
    SessionID   uint64          // Correlates reserve/install phases
    RSNPK       cipher.PubKey   // RSN that authorized this setup
    Nonce       uint64          // Anti-replay, unique per session+hop
    RSNSig      cipher.Sig      // Signature over (Phase || SessionID || RSNPK || Nonce || TargetPK || RuleData)
    RuleData    []byte          // Serialized routing rules for this hop (empty in Reserve phase)
    ReserveN    uint8           // Number of route IDs to reserve (Reserve phase only)
    RelayTpID   uuid.UUID       // Transport to forward Payload on (zero = terminal hop)
    Payload     []byte          // Next hop's CascadeSetup (opaque to this hop)
}
```

### CascadeAck

```
CascadeAck {
    SessionID   uint64
    Phase       uint8           // 0 = Reserve, 1 = Install
    RouteIDs    []RouteID       // Reserved IDs (Reserve phase) or empty (Install phase)
    Error       string          // Non-empty on failure
}
```

### Signature Verification

Each hop verifies the cascade message independently:

1. Check `RSNPK` against the visor's configured `route_setup_nodes` list
2. Compute `hash(Phase || SessionID || RSNPK || Nonce || myPK || RuleData)`
3. Verify `RSNSig` against the hash using `RSNPK`
4. Reject if verification fails — do not install rules, do not relay

This ensures:
- Only a trusted RSN can install rules (authorization)
- The message cannot be modified by relay hops (integrity)
- The message cannot be replayed to a different visor (Nonce + TargetPK binding)
- The relay hop cannot forge messages for its neighbors (lacks RSN SK)

## Protocol Flow

### Phase 1: Reserve Route IDs

The RSN builds a nested reserve message from last hop to first:

```
Route: A --tp1--> C --tp2--> D --tp3--> B

RSN constructs:
  For B (terminal):  {Phase:Reserve, ReserveN:2, RelayTpID:0,   Payload:nil}
  For D:             {Phase:Reserve, ReserveN:2, RelayTpID:tp3, Payload:serialize(B's msg)}
  For C:             {Phase:Reserve, ReserveN:2, RelayTpID:tp2, Payload:serialize(D's msg)}
  Sends to A:        {Phase:Reserve, ReserveN:2, RelayTpID:tp1, Payload:serialize(C's msg)}
```

Each hop:
1. Verifies RSN signature
2. Reserves `ReserveN` local route IDs
3. If `RelayTpID != 0`: sends `Payload` as a `CascadeSetupPacket` on route ID 0 over transport `RelayTpID`
4. Waits for `CascadeAck` from the next hop (bounded timeout, e.g., 10 seconds)
5. Prepends its own reserved route IDs to the ACK
6. Relays the combined ACK back to the previous hop

The ACK cascades back: `B -> D -> C -> A -> RSN`. The RSN now has all reserved route IDs from every hop.

### Phase 2: Install Rules

With all route IDs known, the RSN computes routing rules (same as `GenerateRules` today) and builds a nested install message:

```
RSN constructs:
  For B (terminal):  {Phase:Install, RuleData:[B's consume rule], RelayTpID:0,   Payload:nil}
  For D:             {Phase:Install, RuleData:[D's fwd rule],     RelayTpID:tp3, Payload:serialize(B's msg)}
  For C:             {Phase:Install, RuleData:[C's fwd rule],     RelayTpID:tp2, Payload:serialize(D's msg)}
  Sends to A:        {Phase:Install, RuleData:[A's edge rules],   RelayTpID:tp1, Payload:serialize(C's msg)}
```

Each hop:
1. Verifies RSN signature
2. Installs rules from `RuleData` in its routing table
3. If `RelayTpID != 0`: relays `Payload` on route ID 0 over transport `RelayTpID`
4. Waits for ACK from next hop
5. ACKs back to previous hop

When A receives the final ACK, the route is live.

### Timing

Each hop: ~100-500ms round-trip. For a 5-hop route:
- Reserve phase: ~2.5 seconds (cascade forward + ACK back)
- Install phase: ~2.5 seconds
- Total: ~5 seconds

Well within the 10-minute route keepalive and the RSN's per-request timeout.

## Reaching the RSN (Relay Mechanism)

### Direct Transport

If a visor has a direct transport to the RSN (labeled "setup"), it sends the setup request directly over that transport as an RPC call (same as today over DMSG, but over the transport's raw connection).

### Relay Through a Neighbor

If a visor does not directly transport the RSN:

1. The visor knows the RSN's PK (from config)
2. On first contact, the visor reaches the RSN via DMSG (current behavior)
3. The RSN includes its current transport peer list (`relay_peers`) in the response
4. The visor caches this list locally
5. On subsequent requests, the visor checks: "do I transport any of these relay peers?"
6. If yes: sends the request to that peer on route ID 0 with destination = RSN PK
7. The peer forwards to the RSN over its direct "setup" transport
8. Response returns via the same path

If the cached relay peer list is stale, the request times out and the visor falls back to DMSG, receiving a fresh peer list.

### Multi-Hop Relay

If the visor doesn't transport any relay peer directly, but transports a visor that transports a relay peer, the relay can cascade multiple hops on route ID 0. Each hop checks its own transports for the RSN PK or a known relay peer, and forwards toward the RSN.

## Failure Handling

### Cascade Failures

| Failure | Behavior |
|---------|----------|
| Transport to next hop is dead | Cascade fails at that hop. Error ACK propagates back to RSN. Route setup fails — the broken transport means the route is unusable anyway. |
| RSN signature verification fails | Hop rejects the message. Does not install rules or relay. Returns error ACK. |
| Reserve phase timeout | Session expires. Partially-reserved IDs expire via normal GC. |
| Install phase timeout | Session expires. Partially-installed rules expire via DefaultRouteKeepAlive GC. |
| RSN unreachable via relay | Fall back to DMSG. |

### Mixed-Version Fallback

When the route contains a mix of new visors (cascade-capable) and old visors (not cascade-capable):

1. The RSN knows each hop's capabilities (from `CapCascade` flag in the transport handshake data, or from attempting the cascade).
2. The cascade proceeds along cascade-capable hops from the source toward the destination.
3. When the cascade reaches a hop that cannot relay to the next hop (because the next hop is an old visor that drops `CascadeSetupPacket`), the cascade times out at that hop.
4. The timeout error propagates back to the RSN, identifying the break point.
5. The RSN falls back to DMSG for the failing hop and all subsequent hops in the route.
6. Hops before the break point already have their rules installed via cascade. Those rules remain valid.

This means: cascade what you can from the source, DMSG the rest from the break point onward. As the network updates, the break point moves further along the route until eventually the entire cascade completes without DMSG.

## RSN Standalone Configuration

```json
{
  "version": "1.0",
  "pk": "<RSN public key>",
  "sk": "<RSN secret key>",
  "dmsg": {
    "discovery": "http://dmsg.discovery.skywire.skycoin.com",
    "sessions_count": 6
  },
  "transport": {
    "discovery": "http://tpd.skywire.skycoin.com",
    "address_resolver": "",
    "label": "setup",
    "max_transports": 20,
    "autoconnect": true,
    "public_autoconnect": false
  },
  "cascade": {
    "reserve_timeout": "10s",
    "install_timeout": "10s",
    "session_ttl": "30s"
  },
  "relay_peer_announce": true,
  "log_level": "info"
}
```

| Field | Description |
|-------|-------------|
| `transport.discovery` | Transport discovery URL (transports are registered here with label "setup") |
| `transport.address_resolver` | Empty — RSN does not register with AR (no inbound transports) |
| `transport.label` | Default label for transports created by this visor ("setup") |
| `transport.max_transports` | Maximum transports to maintain |
| `transport.autoconnect` | Enable autoconnect to build connectivity outward |
| `transport.public_autoconnect` | Disabled — RSN does not advertise as a public visor |
| `cascade.reserve_timeout` | Per-hop timeout for the reserve phase |
| `cascade.install_timeout` | Per-hop timeout for the install phase |
| `cascade.session_ttl` | How long to keep session state between phases |
| `relay_peer_announce` | Include transport peer list in setup responses |

## Route Finder Exclusion

The route finder and local route calculation exclude transports with label `"setup"` from data route path calculations. This prevents the RSN from being included as an intermediary hop without requiring PK-based matching.

Additionally, the RSN itself rejects any `AddIntermediaryRules` request — it never accepts forwarding rules. This is a hard enforcement backstop even if a route were somehow calculated through it.

## Backward Compatibility

- Old visors drop unknown packet types on route ID 0 (existing behavior)
- The RSN checks `CapCascade` on hops before attempting cascade
- Mixed-version routes: cascade up to the first old hop, DMSG the rest
- The cascade and DMSG protocols coexist — the RSN selects per-hop based on capabilities
- The embedded RSN continues working over DMSG as today (no cascade support needed)
- Gradual rollout: as visors update, more hops support cascade, less DMSG load

## Implementation Phases

### Phase 1: Core Protocol
- Transport label "setup" (add to `pkg/transport/entry.go`)
- New packet types (`CascadeSetupPacket`, `CascadeAckPacket`)
- Cascade message format and serialization (`pkg/routing/cascade.go`)
- Transport-layer interception in `managed_transport.go`
- Cascade handler on visor side (`pkg/router/cascade_handler.go`)
- Cascade builder on RSN side (`pkg/router/cascade_builder.go`)
- Capability negotiation (`CapCascade`)
- RSN standalone visor with transport manager
- Route finder exclusion of "setup" labeled transports

### Phase 2: Relay and Discovery
- Route ID 0 relay mechanism for reaching the RSN
- Relay peer list in RSN responses
- Relay peer caching on visors
- Multi-hop relay forwarding

### Phase 3: Privacy and Cleanup
- Deprecate embedded RSN (optional, once standalone cascade is proven)
