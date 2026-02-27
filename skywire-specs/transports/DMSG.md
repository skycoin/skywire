# DMSG - Distributed Messaging Transport

DMSG is an overlay network transport that routes traffic through intermediary DMSG servers. It provides reliable connectivity when direct connections (STCPR, SUDPH) are not possible.

## Overview

DMSG creates a messaging overlay network on top of the internet. Visors connect to DMSG servers, which relay traffic between visors. This enables communication even when both visors are behind restrictive NATs or firewalls that block direct connections.

## Architecture

```
┌─────────┐                                              ┌─────────┐
│ Visor A │                                              │ Visor B │
└────┬────┘                                              └────┬────┘
     │                                                        │
     │ Session                                        Session │
     ▼                                                        ▼
┌─────────────┐         ┌─────────────┐         ┌─────────────┐
│ DMSG Server │◄───────►│ DMSG Server │◄───────►│ DMSG Server │
│     #1      │         │     #2      │         │     #3      │
└──────┬──────┘         └──────┬──────┘         └──────┬──────┘
       │                       │                       │
       └───────────────────────┼───────────────────────┘
                               │
                      ┌────────┴────────┐
                      │ DMSG Discovery  │
                      │    Service      │
                      └─────────────────┘
```

## Components

### DMSG Discovery Service

The discovery service maintains a registry of:
- Available DMSG servers and their addresses
- Registered visor public keys and their connected servers

Visors query the discovery to find:
1. DMSG servers to connect to
2. Which server(s) a target visor is connected to

### DMSG Servers

DMSG servers are relay nodes that:
- Accept sessions from visors
- Route streams between connected visors
- Handle connection multiplexing

### DMSG Client (Visor)

Each visor runs a DMSG client that:
- Maintains sessions with one or more DMSG servers
- Registers its presence with the discovery service
- Dials and accepts streams to/from other visors

## Connection Flow

### Initialization

1. Visor creates DMSG client with its key pair
2. Client connects to DMSG discovery service
3. Client establishes sessions with configured number of DMSG servers
4. Client registers with discovery (public key + connected servers)

### Dialing

1. Visor A wants to connect to Visor B
2. A queries discovery for B's connected DMSG servers
3. A dials a stream to B through a shared DMSG server (or relays through multiple servers)
4. Stream is established for bidirectional communication

### Accepting

1. Visor B listens on a DMSG port
2. When A dials, the DMSG server notifies B
3. B accepts the incoming stream
4. Transport handshake completes

## Configuration

```json
{
    "dmsg": {
        "discovery": "https://dmsgd.skywire.dev",
        "sessions_count": 1,
        "servers": [],
        "servers_type": "all",
        "protocol": "tcp"
    }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `discovery` | string | URL of the DMSG discovery service |
| `sessions_count` | integer | Minimum number of DMSG server sessions to maintain |
| `servers` | array | Specific servers to connect to (empty = auto-discover) |
| `servers_type` | string | Server selection: `"all"`, `"public"`, or `"private"` |
| `protocol` | string | Transport protocol: `"tcp"` or `"websocket"` |

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

// Dial connects to a remote visor through DMSG
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

- **Universal connectivity**: Works through any NAT/firewall configuration
- **No port forwarding**: Visors don't need inbound ports open
- **Resilient**: Multiple server sessions provide redundancy
- **Fallback**: Works when STCPR/SUDPH fail

## Limitations

- **Latency**: Traffic routed through servers adds latency
- **Bandwidth**: Server capacity limits throughput
- **Centralization**: Depends on DMSG server infrastructure
- **Cost**: Running DMSG servers requires resources

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
3. Fall back to DMSG (relayed)

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
