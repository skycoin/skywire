# Cascading Route Setup

| Status   | Implemented, off by default                                            |
|----------|------------------------------------------------------------------------|
| Revision | 2026-10-27, skywire 38152beed                                          |
| Code     | `pkg/routing/cascade.go`, `pkg/router/cascade_*.go`, `pkg/router/setup_rpc_gateway.go`, `pkg/router/wrappers.go` |

## Abstract

Cascading route setup installs a route without the route setup node (RSN)
dialing each hop. The RSN signs one nested message per path. The source visor
sends it down its own transports, and each hop reserves route IDs or installs
rules and passes the inner message on. Route setup by the RSN over dmsg
([13](13-Setup_Node.md)) remains the default and the fallback.

## Overview

The visor that wants the route is the source. It calls two RPCs on an RSN and
carries the signed results itself. The RSN never contacts a hop and is never on
the data path. Cascade messages travel as `routing.CascadeSetupPacket` and
`routing.CascadeAckPacket` with route ID 0 on the transports of the route.

## Addressing

The RSN is reached as for any route setup, over dmsg port 36, over a direct
transport labelled `setup`, or through a neighbour of the RSN. The hops are
addressed by the transport ID of each hop, `routing.Hop.TpID`.

## Data Formats

### `routing.CascadeSetup`

One layer per target visor. The target of the outermost layer is the source.
The path is oriented source first for both directions. The reverse path is
reversed and its hops swapped, keeping the transport IDs
(`reverseHopsFromSource`). The binary layout is `CascadeSetup.Marshal`. All
integers are big endian.

| Field | Size | Meaning |
|---|---|---|
| `Phase` | 1 | `CascadePhaseReserve` (0) or `CascadePhaseInstall` (1). |
| `SessionID` | 8 | Correlates the two phases and the acknowledgements. |
| `RSNPK` | 33 | The signing RSN. |
| `Nonce` | 8 | Starts at 1 on the innermost layer and rises by one per layer outward. |
| `RSNSig` | 65 | Signature, see below. |
| `ReserveN` | 1 | Route IDs to reserve. Reserve phase only. |
| `RelayTpID` | 16 | Transport on which to send `Payload`. Zero marks the last hop. |
| `RuleDataLen` | 2 | Length of `RuleData`. |
| `RuleData` | n | `routing.SerializeRules`: a count byte, then each rule with a 2 byte length. Empty in the reserve phase. |
| `PayloadLen` | 2 | Length of `Payload`. |
| `Payload` | n | The next layer, opaque to this hop. |
| `EdgeDesc` | 70 | `routing.RouteDescriptor`. Optional trailing data, zero unless the hop is the responding edge. |

The message is at least `cascadeFixedSize` (132) plus 4 bytes. A reader accepts
a message that ends before `EdgeDesc`, and then `EdgeDesc` is zero.

**Signed fields.** `CascadeSetup.SignablePayload(targetPK)` is
`Phase || SessionID || RSNPK || Nonce || TargetPK || RuleData`. The signature is
`cipher.SignPayload` over the SHA-256 of that value. `TargetPK` is the key of the
visor that receives the layer and is not in the message. `ReserveN`, `RelayTpID`,
`Payload` and `EdgeDesc` are not signed.

`EdgeDesc` is the responding edge's descriptor. A hop with a non zero `EdgeDesc`
(`CascadeSetup.IsEdge`) installs `RuleData[0]` as the forward rule and
`RuleData[1]` as the reverse rule of `routing.EdgeRules` through
`router.IntroduceRules`, which creates the route group. Every other hop saves
its rules.

### `routing.CascadeAck`

`SessionID` (8), `Phase` (1), `RouteIDCount` (1), the route IDs (4 each),
`ErrorLen` (2) and `Error`. A reserving hop prepends its IDs to the ACK it
receives from the next hop. A non empty `Error` is a failure.

## Interfaces

### RPC

The source calls `SetupRPCGateway.CascadeSignReserve` and
`SetupRPCGateway.CascadeSignInstall`. Their types are in [13](13-Setup_Node.md).
Both are registered on an RSN that has a cascade builder. Without one they
return `errCascadeUnavailable`.

## Behaviour

### Enabling

The cascade is off by default. A visor uses it only when
`routing.enable_cascade_route_setup` (`visorconfig.Routing.EnableCascadeRouteSetup`)
is true. The generator sets it with `skywire cli config gen --cascade`, and sets
it by default when it generates a configuration for `js/wasm`. When the setting
is false the visor creates no source side builder and every route uses RSN setup
over dmsg. An RSN needs a `cascade` object in its configuration
(`router.SetupConfig.Cascade`, `CascadeConfig` with `reserve_timeout`,
`install_timeout` and `session_ttl`, 10, 10 and 30 seconds when unset) and a
transport manager of its own to build cascades.

### Source driven setup

`runSourceCascade` runs these steps for a route with at least one hop in each
direction.

1. The source calls `CascadeSignReserve`. The RSN builds, for each direction, a
   reserve message for the source and every hop, with `ReserveN` equal to the
   number of times that visor appears on that path, at least one. It signs each
   layer for its target and returns both messages and their session IDs.
2. The source processes the outer layer itself (`ProcessLocalOrigin`), which
   reserves its IDs and relays the inner payload on `RelayTpID`. The two
   directions run at the same time. Each hop verifies the layer, reserves IDs,
   relays, and returns the ACK with its IDs prepended.
3. The source calls `CascadeSignInstall` with the route, the session IDs and the
   collected route IDs of both directions. The RSN rebuilds the ID reservation
   from the IDs and recomputes all rules with `GenerateRules`. It does not accept
   rules from the source. It returns the install messages and the initiating
   `EdgeRules`.
4. The source processes the outer install layers. The forward install message
   sets `EdgeDesc` on the last layer. The reverse install message sets none.
5. The source returns the initiating `EdgeRules` to the dialer.

A hop accepts a layer when `RSNPK` is in its trusted route setup nodes
(`CascadeHandler.trustedRSNs`) and the signature verifies against the hop's own
key. Otherwise it returns an ACK with `ErrCascadeUntrustedRSN` or
`ErrCascadeSigInvalid`. A hop with `routing.no_transit` refuses an install that
is not an edge install. A hop waits 10 seconds (`defaultTimeout`) for the next
hop's ACK.

The source waits 6 seconds for the reserve ACK and 10 seconds for the install
ACK (`CascadeBuilder.reserveTimeout`, `installTimeout`).

### Capability and fallback

An RSN advertises `cascade-sign` (`CapCascadeSign`) and `transport-query` in
`Capabilities` and `HealthCheck` only when it has a cascade builder. The source
does not check the capability first. It calls the RPC.

- If the RSN answers "can't find method" or "can't find service"
  (`isUnimplementedRPC`), the source falls back to `DialRouteGroup`.
- Over dmsg, any other cascade error is logged and the source falls back to
  `DialRouteGroup`, except when the connection to the RSN is lost, which fails the
  attempt.
- Over a transport virtual stream, any other cascade error fails the attempt.
- When a route installed by cascade fails its data plane handshake, the router
  sets `WithForceLegacyRouteSetup` on the context and the following attempts of
  that dial use RSN setup only.

A visor that is not source driven still processes cascade messages sent to it.

## Security Considerations

A hop installs rules only from an RSN it trusts, and the signature binds the
phase, session, nonce, target and rules to that hop. `EdgeDesc` and `RelayTpID`
are not signed, so a relay can change which action the last hop takes or where a
layer is forwarded, which can break the route. The signed rules cannot be changed.
The RSN recomputes the rules from the reserved IDs, so the source cannot choose
them.

## References

- [13 Setup Node](13-Setup_Node.md)
- [09 Route Setup Process](09-Route_Setup_Process.md)
