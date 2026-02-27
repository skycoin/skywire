# DMSG Transport

DMSG transport provides connectivity between visors using the Distributed Messaging System as the underlying network layer.

For detailed DMSG architecture, protocol, and frame formats, see [05-Messaging_System.md](../specifications/05-Messaging_System.md).

## Overview

The DMSG transport wraps the DMSG client (Messaging Client) to provide a Skywire transport interface. Unlike STCPR and SUDPH which establish direct connections, DMSG routes traffic through DMSG servers (Messaging Servers).

**Key constraint:** Both visors must be connected to the same DMSG server to establish a transport. See the Messaging System spec for details on channel creation between clients.

## Transport Adapter

Skywire wraps `dmsg.Client` to implement the transport `Client` interface:

```go
type dmsgClientAdapter struct {
    dmsgC *dmsg.Client
}
```

### Key Behaviors

| Method | Behavior |
|--------|----------|
| `Dial` | Opens a DMSG stream to remote visor via shared server |
| `Listen` | Listens for incoming DMSG streams on a port |
| `Start` | No-op (DMSG client is already serving) |
| `Close` | No-op (DMSG client may be shared with other components) |

The adapter does **not** close the underlying `dmsg.Client` because it's shared with other Skywire components.

### Stream to Transport Mapping

Each DMSG stream is wrapped as a transport:

```go
type dmsgTransportAdapter struct {
    *dmsg.Stream
}
```

The stream provides:
- `LocalPK()` / `RemotePK()` - Visor public keys
- `LocalPort()` / `RemotePort()` - DMSG ports
- Standard `Read` / `Write` / `Close` operations

## Configuration

DMSG configuration in visor config:

```json
{
    "dmsg": {
        "discovery": "https://dmsgd.skywire.dev",
        "sessions_count": 1,
        "servers": [],
        "servers_type": "all"
    }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `discovery` | string | DMSG Discovery service URL |
| `sessions_count` | integer | Number of DMSG server sessions to maintain |
| `servers` | array | Specific servers to connect to (empty = auto-discover) |
| `servers_type` | string | `"all"`, `"public"`, or `"private"` |

## Code References

- Transport adapter: `pkg/transport/network/dmsg.go`
- DMSG client wrapper: `pkg/dmsgc/dmsgc.go`
- Transport type: `pkg/transport/types/types.go` (`DMSG = "dmsg"`)

## See Also

- [05-Messaging_System.md](../specifications/05-Messaging_System.md) - Full DMSG architecture and protocol
- [STCPR](STCPR.md) - TCP with address-resolver
- [SUDPH](SUDPH.md) - UDP hole-punching
- [STCP](STCP.md) - Direct TCP with PK table
