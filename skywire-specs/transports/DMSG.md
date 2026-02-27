# DMSG - Distributed Messaging Transport

DMSG is an overlay network transport that routes traffic through intermediary DMSG servers. It provides reliable connectivity when direct connections (STCPR, SUDPH) are not possible.

## Overview

DMSG creates a messaging overlay network on top of the internet. Visors connect to DMSG servers, which relay traffic between visors. This enables communication even when both visors are behind restrictive NATs or firewalls that block direct connections.

## Architecture

**Important:** Both visors must be connected to the **same** DMSG server to establish a transport. DMSG servers are currently **not interconnected** - there is no server-to-server routing.

```
                      ┌─────────────────┐
                      │ DMSG Discovery  │
                      │    Service      │
                      └────────┬────────┘
                               │
            ┌──────────────────┼──────────────────┐
            │ Register         │        Register  │
            ▼                  ▼                  ▼
     ┌─────────────┐    ┌─────────────┐    ┌─────────────┐
     │ DMSG Server │    │ DMSG Server │    │ DMSG Server │
     │     #1      │    │     #2      │    │     #3      │
     └──────┬──────┘    └──────┬──────┘    └─────────────┘
            │                  │              (no visors)
            │                  │
     ┌──────┴──────┐    ┌──────┴──────┐
     │             │    │             │
┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐
│ Visor A │  │ Visor B │  │ Visor C │  │ Visor D │
└─────────┘  └─────────┘  └─────────┘  └─────────┘

A ◄──► B  ✓  (same server #1)
C ◄──► D  ✓  (same server #2)
A ◄──► C  ✗  (different servers - NOT supported)
```

## Connectivity Constraint

For two visors to communicate via DMSG transport:

1. Both visors must have an active session with the **same** DMSG server
2. The discovery service is used to find which server(s) a visor is connected to
3. If visors share no common server, DMSG transport **cannot** be established

**Implications:**
- Visors should connect to multiple DMSG servers to increase chances of overlap
- The `sessions_count` config determines how many servers a visor connects to
- Network partitioning can occur if server pools don't overlap

## Components

### DMSG Discovery Service

The discovery service maintains a registry of:
- Available DMSG servers and their addresses
- Registered visor public keys and which server(s) they're connected to

Visors query the discovery to find:
1. DMSG servers to connect to
2. Which server(s) a target visor is connected to

### DMSG Servers

DMSG servers are relay nodes that:
- Accept sessions from visors
- Route streams between visors connected to that same server
- Handle connection multiplexing
- Do **not** communicate with other DMSG servers

### DMSG Client (Visor)

Each visor runs a DMSG client that:
- Maintains sessions with one or more DMSG servers
- Registers its presence with the discovery service
- Dials and accepts streams to/from other visors (on shared servers)

## Connection Flow

### Initialization

1. Visor creates DMSG client with its key pair
2. Client queries DMSG discovery for available servers
3. Client establishes sessions with `sessions_count` DMSG servers
4. For each session, client registers with discovery (public key + server)

### Dialing

1. Visor A wants to connect to Visor B
2. A queries discovery for B's connected DMSG servers
3. A checks if it shares any server with B
4. If a shared server exists:
   - A dials a stream to B through that shared server
   - Stream is established for bidirectional communication
5. If no shared server exists:
   - Dial fails - DMSG transport not possible

### Accepting

1. Visor B listens on a DMSG port
2. When A dials (through shared server), the server notifies B
3. B accepts the incoming stream
4. Transport is ready for use

## Configuration

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
| `discovery` | string | URL of the DMSG discovery service |
| `sessions_count` | integer | Number of DMSG server sessions to maintain (higher = better connectivity) |
| `servers` | array | Specific servers to connect to (empty = auto-discover from discovery) |
| `servers_type` | string | Server selection: `"all"`, `"public"`, or `"private"` |

### Session Count Recommendations

| sessions_count | Use Case |
|----------------|----------|
| 1 | Minimal - may have connectivity issues |
| 2-3 | Recommended - good balance of connectivity and resources |
| 5+ | High availability - connects to many servers |

Higher session counts increase the probability of sharing a server with any given visor, but consume more resources.

### Server Types

| Type | Description |
|------|-------------|
| `all` | Connect to any available servers |
| `public` | Only connect to public DMSG servers |
| `private` | Only connect to private/whitelisted servers |

## DMSG Addressing

DMSG uses its own addressing scheme:

```
dmsg://<public-key>:<port>
```

**Example:**
```
dmsg://02a1b2c3d4e5f6...:80
```

## Stream Multiplexing

A single DMSG session can carry multiple streams:
- Each stream is identified by local and remote address (PK:port)
- Streams are independent and can be opened/closed separately
- Efficient use of server connections

## Transport Adapter

Skywire wraps the DMSG client to conform to its transport interface:

```go
type dmsgClientAdapter struct {
    dmsgC *dmsg.Client
}

// Dial connects to a remote visor through DMSG (requires shared server)
func (c *dmsgClientAdapter) Dial(ctx context.Context, remote cipher.PubKey, port uint16) (Transport, error)

// Listen accepts incoming DMSG connections
func (c *dmsgClientAdapter) Listen(port uint16) (Listener, error)
```

The adapter does not close the underlying `dmsg.Client` since it may be shared with other components (e.g., dmsghttp for service communication).

## Session Management

| Parameter | Typical Value | Description |
|-----------|---------------|-------------|
| Min sessions | 1 | Minimum server connections to maintain |
| Reconnect | Automatic | Client reconnects if session drops |
| Discovery refresh | Periodic | Client refreshes server list from discovery |

## Advantages

- **NAT traversal**: Works through any NAT/firewall configuration
- **No port forwarding**: Visors don't need inbound ports open
- **Fallback**: Works when STCPR/SUDPH fail

## Limitations

- **Same-server requirement**: Both visors must share a DMSG server
- **No server mesh**: Servers don't route to each other (yet)
- **Latency**: Traffic routed through servers adds latency
- **Bandwidth**: Server capacity limits throughput
- **Centralization**: Depends on DMSG server infrastructure

## Future: Server-to-Server Routing

Server interconnection is planned but not yet implemented. When available:
- Servers will form a mesh network
- Visors won't need to share the same server
- Traffic will route through multiple servers if needed

## Use Cases

### Primary Transport

When direct connections aren't possible:
- Both visors behind symmetric NAT
- Restrictive corporate firewalls
- Mobile networks with carrier-grade NAT

### Fallback Transport

As backup when preferred transports fail:
1. Try SUDPH (direct UDP hole-punch)
2. Try STCPR (direct TCP via resolver)
3. Fall back to DMSG (relayed, requires shared server)

### Service Communication

DMSG is also used for internal Skywire service communication:
- Transport setup coordination
- Hypervisor-to-visor management
- Route setup protocol

## Code References

- Transport adapter: `pkg/transport/network/dmsg.go`
- DMSG client config: `pkg/dmsgc/dmsgc.go`
- DMSG package: `github.com/skycoin/dmsg/pkg/dmsg`
- Transport type constant: `pkg/transport/types/types.go:17`

## External Resources

- [DMSG Repository](https://github.com/skycoin/dmsg) - DMSG protocol implementation
- [DMSG Discovery](https://github.com/skycoin/dmsg/tree/master/cmd/dmsg-discovery) - Discovery service

## See Also

- [STCPR](STCPR.md) - TCP with address-resolver (preferred for direct connections)
- [SUDPH](SUDPH.md) - UDP hole-punching (best for NAT traversal)
- [STCP](STCP.md) - Direct TCP with local PK table
