# Router

The `Router` (`pkg/router/router.go`) manages the visor's routing table, handles incoming packets from transports, and coordinates route setup with trusted Route Setup Nodes.

## Responsibilities

1. **Packet dispatch** — reads packets from all managed transports and routes them based on routing rules:
   - **DataPacket, HandshakePacket** — forwarded to the route group identified by the route ID
   - **ClosePacket** — closes the route group
   - **KeepAlivePacket** — forwarded if intermediary, consumed if edge
   - **PingPacket, PongPacket, ErrorPacket** — dispatched to the route group
   - **SACKPacket** — handled by the selective acknowledgment subsystem
   - **TransportPingPacket, TransportPongPacket** — intercepted at the transport layer before reaching the router

2. **Route group management** — maintains a map of active route groups indexed by `RouteDescriptor` (src PK, dst PK, src port, dst port). Route groups are created when edge rules are installed by the RSN.

3. **Routing table** — a key-value store mapping `RouteID` → `RoutingRule`. Rules have a `KeepAlive` duration (default 24 hours); expired rules are garbage-collected periodically.

4. **Route setup coordination** — when an application requests a connection to a remote visor, the router:
   a. Queries the Route Finder for candidate paths (or calculates locally if `force_local_routes` is enabled)
   b. Sends a `DialRouteGroup` RPC to a trusted RSN
   c. Receives the initiating edge rules and installs them
   d. Performs the Noise handshake with the remote visor

5. **Local route calculation** — optionally computes routes from locally synced transport discovery data, bypassing the Route Finder service. Enabled via `SetForceLocalRoutes(true)`.

## Port Management

The router uses a `Porter` (`pkg/skywire-utilities/pkg/netutil/porter.go`) for port allocation:

- Ports 0-10 are reserved for system services
- Well-known ports are reserved by listeners (e.g., apps, setup node)
- Ephemeral ports (49152-65535) are allocated dynamically for outgoing connections
- `ReserveEphemeral` allocates a port and returns a freeing function
- `ResetEphemeral` recovers from port exhaustion by clearing all ephemeral reservations

## Packet Forwarding

When a packet arrives on a transport:

1. Extract the `RouteID` from the packet header
2. Look up the routing rule in the routing table
3. If the rule is **Forward** or **IntermediaryForward**: reconstruct the packet with the next-hop route ID and write it to the next transport
4. If the rule is **Consume**: deliver the packet to the local route group

For intermediary nodes, the packet payload is opaque (Noise-encrypted). The intermediary only reads the route ID from the header and forwards blindly.

## Configuration

```json
{
  "routing": {
    "route_setup_nodes": ["<pk1>", "<pk2>"],
    "route_finder": "http://rf.skywire.skycoin.com",
    "route_finder_dmsg": "dmsg://<pk>:80",
    "route_finder_timeout": "10s",
    "min_hops": 1
  }
}
```

| Field | Description |
|---|---|
| `route_setup_nodes` | Trusted RSN public keys (visor only accepts setup from these) |
| `route_finder` | HTTP URL for the Route Finder service |
| `route_finder_dmsg` | DMSG URL for the Route Finder service |
| `route_finder_timeout` | Timeout for Route Finder queries |
| `min_hops` | Minimum number of hops for route calculation |

`min_hops` and `force_local_routes` are changeable at runtime via RPC.
