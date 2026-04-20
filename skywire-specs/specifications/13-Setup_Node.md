# Setup Node

The *Route Setup Node* (RSN) is located in `pkg/router/setupnode.go`. It coordinates the establishment of bidirectional routes between visors by connecting to each visor along the route path, reserving route IDs, generating routing rules, and distributing them.

## Identity

The RSN is identified by a secp256k1 public key. Visors include trusted RSN public keys in their `routing.route_setup_nodes` config. The visor's router only accepts route setup connections from trusted RSN PKs (checked in `router_serve.go:SetupIsTrusted`).

## Deployment Modes

### Standalone RSN

Runs as a dedicated service process (`cmd/svc/setup-node/`). Listens on DMSG port 36 (`DmsgSetupPort`) for incoming route setup requests from visors. Has its own DMSG client identity, connection pool, and metrics collector.

### Embedded RSN

Runs within a visor process (`pkg/visor/embedded_route_setup.go`). Uses a separate DMSG client with its own PK/SK (configured via `routing.route_setup_sk`). Listens on DMSG port 36. Shares the visor's DMSG server sessions but has an independent porter (ephemeral port space).

## Route Setup Protocol

The RSN communicates with visors via Go `net/rpc` over DMSG streams on port 136 (`DmsgAwaitSetupPort`).

### RPC Interface (`SetupRPCGateway`)

The RSN exposes a single RPC method:

- **DialRouteGroup(BidirectionalRoute) → EdgeRules** — sets up a bidirectional route and returns the initiating edge's routing rules.

Internally, `DialRouteGroup` calls `CreateRouteGroup` which performs:

1. **Validate** the `BidirectionalRoute` input.
2. **Circuit breaker check** — if the destination has accumulated too many consecutive failures, fast-fail with `ErrCircuitOpen`.
3. **Reserve route IDs** — connect to all visors along both forward and reverse paths via DMSG port 136, call `ReserveIDs` on each to allocate route IDs.
4. **Generate routing rules** — create Forward, IntermediaryForward, and Consume rules using the reserved route IDs.
5. **Broadcast intermediary rules** — send IntermediaryForward rules to all intermediary visors.
6. **Broadcast responding edge rules** — send Forward + Consume rules to the destination visor via `AddEdgeRules`.
7. **Return initiating edge rules** — return Forward + Consume rules to the requesting visor.

### Connection Management

The RSN uses a `ClientPool` (`pkg/router/client_pool.go`) to reuse DMSG connections across route setup requests. Connections are keyed by remote PK with a 5-minute idle TTL. Per-RPC deadlines (30 seconds) are set on each call, then cleared after completion so pooled connections can idle.

The standalone RSN limits concurrent handler goroutines to 512 (`maxConcurrentHandlers`). Each handler has a 70-second hard deadline.

### Metrics

The RSN collects per-request statistics via a `Collector` (`pkg/router/setupmetrics/stats.go`):

- Total, successful, failed request counts
- Failure breakdown by reason (source_unreachable, circuit_open, id_reservation, destination_rules, context_deadline, etc.)
- Latency percentiles (min, mean, p50, p95, p99, max) over a ring buffer of the last 1024 successful setups
- Route length histogram (hop count distribution)
- Per-destination stats with circuit breaker state (closed, open, half_open)
- Recent failure ring buffer (last 128 failures with src/dst PK, hop count, reason, error, duration)

The standalone RSN exposes metrics via DMSG HTTP `/stats` endpoint. The embedded RSN exposes them via the visor's `RouteSetupStats` RPC.

### Circuit Breaker

Per-destination circuit breakers prevent repeated dial attempts to known-bad visors:

- **Closed** (normal) — requests proceed
- **Open** — requests fast-fail with `ErrCircuitOpen` after `failureThreshold` (default 5) consecutive failures
- **Half-open** — after `circuitMaxOpenDuration` (10 minutes), one probe request is allowed; success resets to closed, failure re-opens

The breaker distinguishes source-side failures (dial to source visor failed) from destination-side failures to avoid penalizing a healthy destination when the source is unreachable.

## Visor-Side Route Setup

When a visor receives route setup rules from the RSN (via `AddEdgeRules` RPC on port 136), it:

1. Creates a raw `RouteGroup` and installs the forward and reverse routing rules.
2. Performs a Noise protocol handshake with the remote edge visor over the route.
3. On handshake success, wraps the route group in a `NoiseRouteGroup` for encrypted communication.
4. Capability negotiation during handshake enables optional features (CapMux, CapSACK).

The visor only accepts `AddEdgeRules` from PKs in its `routing.route_setup_nodes` config list.
