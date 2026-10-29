# Setup Node

| Status   | Implemented                                                       |
|----------|-------------------------------------------------------------------|
| Revision | 2026-10-27, skywire f37a464a4 |
| Code     | `pkg/router/setupnode.go`, `pkg/router/setup_rpc_gateway.go`, `pkg/router/rpc_gateway.go`, `pkg/router/setupmetrics`, `pkg/visor/embedded_route_setup.go`, `cmd/svc/setup-node` |

## Abstract

A route setup node (RSN) builds a multi-hop route on behalf of a visor. It
reserves route IDs on every visor of the path, generates the routing rules,
installs them, and returns the rules of the initiating edge. It can also sign
the messages of a source driven cascade ([28](28-Cascading_Route_Setup.md)) and
sign transport queries ([04](04-Transport_Discovery.md)).

## Overview

The visor sends a request to an RSN. The RSN calls each visor of the route on
the visor's own RPC service. A visor accepts calls only from keys in its
`routing.route_setup_nodes` list. The route finder ([08](08-Route_Finder.md))
supplies the path the caller puts in the request.

## Addressing

| Port | Identifier | Listener | Purpose |
|---|---|---|---|
| 36 | `skyenv.DmsgSetupPort` | the RSN | Requests to the RSN. |
| 136 | `skyenv.DmsgAwaitSetupPort` | each visor | Calls from the RSN to a visor. |

Both are dmsg ports. The RSN keys are `route_setup_nodes` in
`deployment.Services`. A standalone RSN runs from `cmd/svc/setup-node`. A visor
MAY embed one (`pkg/visor/embedded_route_setup.go`) that has its own dmsg client
and key, `routing.route_setup_sk`, and listens on port 36 as well. A visor with a
direct transport labelled `setup` to an RSN can send the same requests as
`routing.SetupRPCPacket` virtual streams when the RSN has cascade enabled.

## Interfaces

### RPC

Both services are Go `net/rpc` services over gob (`github.com/0magnet/gobrpc`).
Registration code is generated in `pkg/router/rpc_register.go`.

#### `SetupRPCGateway` on port 36

The service name is `SetupRPCGateway` (`rpcName`). The handlers do not compare
the caller key with a list. `CapabilitiesReply.Caps` and `HealthCheckReply.Caps`
carry the capability names below.

| Method | Request | Reply | Behaviour |
|---|---|---|---|
| `Capabilities` | `CapabilitiesArgs{}` | `CapabilitiesReply{Caps []string}` | Capability names. |
| `HealthCheck` | `HealthCheckArgs{}` | `HealthCheckReply{Status, Caps, Version, Commit, Date}` | `Status` is `OK`. |
| `DialRouteGroup` | `routing.BidirectionalRoute` | `routing.EdgeRules` | Sets up one route group. |
| `DialRouteGroupBatch` | `routing.BidirectionalRouteBatch` | `routing.BidirectionalRouteBatchReply` | Sets up up to `routing.MaxBatchRoutes` (32) routes. |
| `CascadeSignReserve` | `CascadeSignReserveArgs{Route}` | `CascadeSignReserveReply{FwdSessionID, FwdReserveBytes, RevSessionID, RevReserveBytes}` | Signs the reserve cascades. |
| `CascadeSignInstall` | `CascadeSignInstallArgs{Route, FwdSessionID, RevSessionID, FwdRouteIDs, RevRouteIDs}` | `CascadeSignInstallReply{FwdInstallBytes, RevInstallBytes, InitEdge}` | Signs the install cascades. |
| `SignTransportQuery` | `SignTransportQueryArgs{RequesterPK, TargetPK}` | `SignTransportQueryReply{Query *TransportQuery}` | Signs a transport query with a random nonce. |
| `RelayPeers` | `RelayPeersArgs{}` | `RelayPeersReply{Peers []PubKey}` | Remote keys of the RSN's open transports. Empty without cascade. |

Capability names: `CapBatchRouteSetup` (`batch-route-setup`) is always
advertised. `CapCascadeSign` (`cascade-sign`) and `CapTransportQuery`
(`transport-query`) are advertised only when the RSN has a `CascadeBuilder`. The
three methods behind them return `errCascadeUnavailable` otherwise. A client
MUST send `DialRouteGroupBatch` only to an RSN that advertised
`batch-route-setup`. A node that has no `Capabilities` method answers with the
`net/rpc` method not found error, and the client then sends only
`DialRouteGroup`.

`BidirectionalRoute` is `Desc` (`RouteDescriptor`), `KeepAlive` (duration),
`Forward` and `Reverse` (arrays of `routing.Hop`: `TpID`, `From`, `To`, and
optional `Latency`, `Type`, `ThroughputBps`). `EdgeRules` is `Desc`, `Forward`
and `Reverse` rules. A batch has `Routes` of `BatchRouteRequest{ID, Route}`. All
members MUST share one source and one destination and have distinct IDs. Each
result is `BatchRouteResult{ID, Rules, Error}`. A non empty `Error` fails that
member alone. A member missing from the reply was not attempted.
`BidirectionalRouteBatch.Check` returns `ErrBatchEmpty`, `ErrBatchTooLarge`,
`ErrBatchDuplicateID` or `ErrBatchMixedEndpoints`.

#### `RPCGateway` on port 136

The service name is `RPCGateway` (`router.RPCName`). A visor accepts the stream
from a key in its trusted route setup nodes (`router.SetupIsTrusted`). A visor
also accepts the stream from a peer it has a transport with, for a route between
that peer and itself without an RSN. That caller gets only `ReserveIDs`, at most
`maxDirectSetupIDs` (4) IDs once, and `AddEdgeRules` once, for a rule that leaves
over a transport to the caller (`directSetupGateway`). The stream has a deadline
of two minutes.

| Method | Request | Reply | Behaviour |
|---|---|---|---|
| `ReserveIDs` | `uint8` count | `[]routing.RouteID` | Reserves route IDs. |
| `AddEdgeRules` | `routing.EdgeRules` | `bool` | Installs the edge rules and starts the route group handshake. |
| `AddIntermediaryRules` | `[]routing.Rule` | `bool` | Installs transit rules. Refused only when the visor has `routing.no_transit`. |
| `DelRules` | `[]routing.RouteID` | `bool` | Removes rules. Always reports success. |

## Behaviour

### Route setup

`DialRouteGroup` calls `CreateRouteGroup`.

1. `BidirectionalRoute.Check` validates the route.
2. The per destination circuit breaker is consulted when enabled.
3. `ReserveRouteIDs` calls `ReserveIDs` on every visor of both paths.
4. `GenerateRules` makes the Forward, Consume and intermediary rules.
5. `BroadcastIntermediaryRules` installs the transit rules. A failure removes
   the rules already installed (`installedRules.teardown`).
6. `AddEdgeRules` is called on the destination.
7. The initiating edge rules are returned.

`DialRouteGroupBatch` reserves once per distinct hop and installs once per
distinct hop for all members. It runs under a deadline of `n * Timeout / 2`,
at most `4 * Timeout`, for `n` members above one.

### Limits

| Value | Identifier |
|---|---|
| 512 concurrent request handlers | `maxConcurrentHandlers` |
| 30 seconds per request | `timeout` in `Node.Serve`, `SetupRPCGateway.Timeout` |
| 70 seconds per handler | `handlerTimeout`, `2*timeout + 10s`, also a connection deadline |
| 10 seconds to dial a visor | `router.DialTimeout` |
| 30 seconds per call to a visor | `rpcDeadline` |
| 105 seconds idle in the connection pool | `DefaultPoolTTL`, `streamIdleTimeoutForPool - 15s` |
| 30 seconds between pool evictions | `ClientPool.evictLoop` |
| 5 minutes maximum age of an ephemeral dmsg port | `porterMaxAge`, swept every 60 seconds |

An embedded RSN uses a request timeout of 2 minutes. `ClientPool` holds one
connection per remote key and discards a connection that has idled past the
limit.

### Circuit breaker

The breaker is per destination key. It is off by default
(`CircuitBreakerEnabledDefault`). The live values come from the router settings
catalog (`pkg/router/routersettings`) and can be changed without a restart.

| Setting | Default | Go identifier of the default |
|---|---|---|
| `setup.circuit_breaker` | false | `CircuitBreakerEnabledDefault` |
| `setup.circuit_fail_threshold` | 3 | `CircuitFailureThresholdDefault` |
| `setup.circuit_open_duration` | 5 minutes | `CircuitOpenDurationDefault` |
| `setup.circuit_max_open_duration` | 30 minutes | `CircuitMaxOpenDurationDefault` |
| `setup.circuit_fail_window` | 5 minutes | `CircuitFailureWindowDefault` |

Consecutive failures to a destination that fall within the window trip the
breaker to `open`. After the open duration it becomes `half_open` and admits one
probe. A success closes it and a failure opens it again. After the maximum open
duration the breaker is forced closed. An open breaker fails a setup with
`ErrCircuitOpen`. A failure to reach the source or an intermediate visor is not
counted against the destination. When the breaker is disabled, failures are
still classified and counted. Other `setup.*` settings are `setup.batch_window`
(default 40 ms), `setup.batch_max` (16), `setup.fill_inflight` (8),
`setup.plan_claim_ttl` (20 s) and `setup.first_hop_filter_max` (8).

### Statistics

`setupmetrics.Collector` counts requests by outcome, by failure reason
(`FailureReason`), and by path (`SetupKind`: `single`, `batch`, `cascade_sign`).
It keeps the latencies of the last 1024 setups (`defaultLatencyRingSize`), the
last 128 failures (`defaultFailureRingSize`) and up to 512 destinations
(`defaultDestCapacity`), with their breaker state.

## Security Considerations

A visor installs rules only from a trusted RSN key, authenticated by the dmsg
handshake. The RSN can install any rule on a visor that trusts it. The RSN does
not authenticate the callers of its own service, so a caller can ask an RSN to
build a route between any two visors that trust it, and to sign a cascade or a
transport query. The cascade install RPC recomputes the rules from the route and
the IDs and does not accept rules from the caller.

## References

- [28 Cascading Route Setup](28-Cascading_Route_Setup.md)
- [09 Route Setup Process](09-Route_Setup_Process.md)
