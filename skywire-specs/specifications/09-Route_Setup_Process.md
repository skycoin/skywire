# Route Setup Process

Route setup is coordinated by a trusted Route Setup Node (RSN). The process establishes a bidirectional route between two visors (source and destination) across one or more intermediate hops.

## Protocol

1. The source visor sends a `DialRouteGroup` RPC to the RSN with a `BidirectionalRoute` containing the forward path (source → destination) and reverse path (destination → source). Each path is a sequence of hops, where each hop references a transport ID.

2. The RSN checks the per-destination circuit breaker. If the destination has too many recent failures, the request is fast-failed with `ErrCircuitOpen`.

3. The RSN connects to every visor along both paths via DMSG port 136 (`DmsgAwaitSetupPort`). Connections are reused from the `ClientPool` when available; fresh dials use a 10-second timeout.

4. The RSN sends `ReserveIDs` to each visor to reserve the required number of route IDs. The number of IDs per visor is determined by how many rules that visor will receive.

5. The RSN generates routing rules:
   - For the forward path `A → X → Y → B`:
     - Visor A: `ForwardRule(routeID_A, routeID_X, transportID_AX, srcPK=A, dstPK=B, srcPort, dstPort)`
     - Visor X: `IntermediaryForwardRule(routeID_X, routeID_Y, transportID_XY)`
     - Visor Y: `IntermediaryForwardRule(routeID_Y, routeID_B, transportID_YB)`
     - Visor B: `ConsumeRule(routeID_B, srcPK=A, dstPK=B, srcPort, dstPort)`
   - For the reverse path `B → Y → X → A`: same pattern in reverse.

6. The RSN broadcasts `IntermediaryForward` rules to intermediary visors (X, Y) concurrently.

7. The RSN sends the responding edge rules (Forward + Consume) to visor B via `AddEdgeRules`.

8. The RSN returns the initiating edge rules (Forward + Consume) to visor A as the RPC response.

## Post-Setup Handshake

After the RSN distributes the rules, the two edge visors (A and B) perform a Noise protocol handshake over the newly established route:

1. The initiator (A) sends a `HandshakePacket` with encryption flag and capability bitmap.
2. The responder (B) receives the handshake, negotiates capabilities, and sends its own `HandshakePacket`.
3. Both sides wrap the route group in a Noise-encrypted `NoiseRouteGroup`.

The handshake has a timeout (`handshakeAwaitTimeout`). If the remote visor is old (doesn't support encryption), the timeout expires and the route proceeds unencrypted.

## Failure Handling

- If any `ReserveIDs` call fails, the entire setup fails. Connections used in the attempt are discarded from the pool.
- If `AddEdgeRules` on the destination fails, the setup fails and reserved IDs on other visors expire via their routing rule TTL.
- On success, connections are returned to the pool for reuse.
- The circuit breaker tracks consecutive failures per destination PK and opens after the threshold is reached, preventing further dial attempts for a cooldown period.

## Route Keepalive

Routes have a `KeepAlive` duration (default 24 hours). The `servicePacketLoop` on each route group sends periodic `KeepAlivePacket` frames at the configured interval. Rules that are not refreshed within their keepalive window are garbage-collected by the routing table's expiry sweep.
