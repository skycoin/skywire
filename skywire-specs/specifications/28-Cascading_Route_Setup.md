# Cascading Route Setup

## Overview

Cascading Route Setup eliminates the Route Setup Node's (RSN) dependency on DMSG for installing routing rules on intermediate hops. Instead of the RSN independently dialing every hop via DMSG, it constructs a nested message that cascades along the route's own pre-existing transports using the route ID 0 control channel.

This specification also covers:
- DHT synchronization over transports (same route ID 0 mechanism)
- Address resolver privacy controls for visors
- RSN standalone visor configuration with transport manager

## Motivation

Currently, route setup requires DMSG connectivity between the RSN and every visor in the route. If DMSG is unavailable, no routes can be established — the entire network depends on DMSG servers being online. DMSG was designed as a bootstrapping layer, not a permanent dependency.

The cascade protocol moves route setup to the transport layer:
- The RSN is reachable via transport-level relay (route ID 0)
- Routing rules are installed hop-by-hop over the route's own transports
- DMSG becomes a fallback for initial RSN contact, not a hard requirement
- The same route ID 0 mechanism extends to DHT synchronization

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
- A transport manager (accepts STCPR connections)
- DMSG connectivity (as any visor, for bootstrapping)
- No hosted applications
- No autoconnect
- No participation as an intermediary hop in any data route

The RSN's transports are registered in transport discovery (TD) so the network topology is visible and continuous. However, the route finder and local route calculation **exclude RSN PKs from intermediary positions** in data routes, using the visor's configured `route_setup_nodes` list as the filter. RSN PKs may appear as source or destination (for control traffic) but never as a relay hop.

### Transport Capacity Management

The RSN accepts a configurable maximum number of direct transports (`max_transports`). Once the threshold is reached, the RSN deregisters from the address resolver to stop accepting new incoming transports. Existing transports are maintained.

Visors that cannot directly transport the RSN reach it via relay through a peer that can (see "Relay to RSN" below).

### Address Resolver Privacy Controls (Visors)

Visors gain an optional `ar_transport_limit` configuration field that controls address resolver registration:

| Value | Behavior |
|-------|----------|
| `0` (default) | Normal behavior — stay registered indefinitely |
| `N > 0` | Deregister from AR after `N` transports are established |
| `N < 0` | Never register with AR at all |

When a visor deregisters (or never registers), it cannot receive new incoming transport connections via AR. Existing transports remain functional. The visor is still reachable via DMSG and through already-established transports.

**Interaction with public visor mode:** A visor configured as a public visor (`public_autoconnect: true`) MUST register with AR (otherwise no one can find it). If `ar_transport_limit < 0` conflicts with public visor mode, the visor logs a warning and ignores the limit.

**Interaction with autoconnect:** A visor that has deregistered from AR can still INITIATE outbound transports to public visors and other peers. It only stops accepting unsolicited inbound connections.

## Packet Types

Two new packet types are added to the transport frame format, both using route ID 0:

| Type | Value | Description |
|------|-------|-------------|
| `CascadeSetupPacket` | `10` | Carries a cascade setup message (reserve or install phase) |
| `CascadeAckPacket` | `11` | Carries a cascade acknowledgment/confirmation |
| `DHTPacket` | `12` | Carries a DHT RPC message (Ping, FindNode, GetValue, PutValue) |

All three use route ID 0 and are intercepted at the transport layer before reaching the router. Old visors that don't recognize these types drop them silently (existing behavior for unknown packet types on route ID 0).

### Capability Negotiation

A new capability flag is added to the transport handshake:

```
CapCascade = 1 << 2
```

Visors that support the cascade protocol advertise this during the transport handshake. The RSN checks this flag before attempting cascade setup; if any hop lacks the capability, the RSN falls back to DMSG-based setup for the entire route.

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

The ACK cascades back: `B → D → C → A → RSN`. The RSN now has all reserved route IDs from every hop.

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

If a visor has a direct transport to the RSN, it sends the setup request directly over that transport as an RPC call (same as today over DMSG, but over the transport's raw connection).

### Relay Through a Neighbor

If a visor does not directly transport the RSN:

1. The visor knows the RSN's PK (from config)
2. On first contact, the visor reaches the RSN via DMSG (current behavior)
3. The RSN includes its current transport peer list (`relay_peers`) in the response
4. The visor caches this list locally
5. On subsequent requests, the visor checks: "do I transport any of these relay peers?"
6. If yes: sends the request to that peer on route ID 0 with destination = RSN PK
7. The peer forwards to the RSN over its direct transport
8. Response returns via the same path

If the cached relay peer list is stale (relay peer no longer transports RSN), the request times out and the visor falls back to DMSG, receiving a fresh peer list.

### Multi-Hop Relay

If the visor doesn't transport any relay peer directly, but transports a visor that transports a relay peer, the relay can cascade multiple hops on route ID 0. Each hop checks its own transports for the RSN PK or a known relay peer, and forwards toward the RSN.

## Failure Handling

| Failure | Behavior |
|---------|----------|
| Transport to next hop is dead | Cascade fails at that hop. CascadeAck with error propagates back. Route setup fails. Visor retries with a different path. |
| Intermediate hop doesn't support cascade | RSN detects via capability flag before starting. Uses DMSG-based setup for the entire route. |
| RSN signature verification fails | Hop rejects the message. Does not install rules. Does not relay. Returns error ACK. |
| Reserve phase timeout | Session expires. Any partially-reserved IDs expire via normal GC. |
| Install phase timeout | Session expires. Partially-installed rules expire via normal GC (DefaultRouteKeepAlive). |
| RSN unreachable via relay | Fall back to DMSG. |

**No DMSG fallback for individual hops in the cascade.** If a transport in the route can't carry the cascade message, that transport is broken and the route would be unusable anyway. Fail fast, let the visor retry with a different path.

## DHT Over Transports

The same route ID 0 mechanism extends to DHT synchronization. Currently, DHT RPC (Ping, FindNode, GetValue, PutValue) runs exclusively over DMSG (port 100). A new `DHTPacket` type on route ID 0 allows DHT messages to hop between transport peers without DMSG.

### Transport-Layer DHT

Each visor's DHT node gains a second transport implementation (`TransportLayerDHT`) alongside the existing `DMSGTransport`:

- DHT messages are sent on route ID 0 as `DHTPacket` frames
- Each transport peer is a potential DHT peer
- The routing table is populated from both DMSG-discovered peers and transport peers
- Lookups try transport peers first, fall back to DMSG

This means the DHT can bootstrap and sync purely over transports when DMSG is unavailable, making the entire network more resilient to DMSG outages.

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
    "stcpr_addr": ":7780",
    "address_resolver": "http://ar.skywire.skycoin.com",
    "max_transports": 20,
    "deregister_threshold": 20
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
| `transport.stcpr_addr` | STCPR listen address for direct transport connections |
| `transport.address_resolver` | AR address for transport registration |
| `transport.max_transports` | Maximum direct transport peers to accept |
| `transport.deregister_threshold` | Deregister from AR after this many transports (0 = never) |
| `cascade.reserve_timeout` | Per-hop timeout for the reserve phase |
| `cascade.install_timeout` | Per-hop timeout for the install phase |
| `cascade.session_ttl` | How long to keep session state between phases |
| `relay_peer_announce` | Include transport peer list in setup responses |

## Visor Configuration Changes

### Address Resolver Privacy

New field in visor config (`transport` section):

```json
{
  "transport": {
    "ar_transport_limit": 0
  }
}
```

| Value | Behavior |
|-------|----------|
| `0` | Normal — stay registered with AR indefinitely (default) |
| `N > 0` | Deregister from AR after `N` transports are established |
| `N < 0` | Never register with AR |

**Conflict resolution with public visor mode:**
- If `public_autoconnect: true` and `ar_transport_limit < 0`: log warning, ignore limit (public visors must be discoverable)
- If `public_autoconnect: true` and `ar_transport_limit > 0`: allowed (accept N transports then go dark — useful for bandwidth-limited public visors)

### Route Finder Exclusion

No new configuration needed. The visor's existing `route_setup_nodes` list is used to exclude RSN PKs from intermediary positions in route calculations. Both the route finder service and local route calculation apply this filter.

## Backward Compatibility

- Old visors drop unknown packet types on route ID 0 (existing behavior)
- The RSN checks `CapCascade` on all hops before attempting cascade
- If any hop lacks cascade support, the RSN uses DMSG-based setup (current protocol)
- The cascade and DMSG protocols coexist — the RSN selects per-request based on path capabilities
- The embedded RSN continues working over DMSG as today (no cascade support needed)
- Gradual rollout: as visors update, more routes use cascade, less DMSG load

## Implementation Phases

### Phase 1: Core Protocol
- New packet types (`CascadeSetupPacket`, `CascadeAckPacket`)
- Cascade message format and serialization (`pkg/routing/cascade.go`)
- Transport-layer interception in `managed_transport.go`
- Cascade handler on visor side (`pkg/router/cascade_handler.go`)
- Cascade builder on RSN side (`pkg/router/cascade_builder.go`)
- Capability negotiation (`CapCascade`)
- RSN transport manager and config

### Phase 2: Relay and Discovery
- Route ID 0 relay mechanism for reaching the RSN
- Relay peer list in RSN responses
- Relay peer caching on visors
- Multi-hop relay forwarding

### Phase 3: DHT Over Transports
- `DHTPacket` type on route ID 0
- `TransportLayerDHT` transport implementation
- Dual-transport DHT node (DMSG + transport layer)
- Transport peer integration into DHT routing table

### Phase 4: Privacy and Cleanup
- `ar_transport_limit` visor config
- AR deregistration logic
- Route finder RSN exclusion
- Deprecate embedded RSN (optional, once standalone cascade is proven)
