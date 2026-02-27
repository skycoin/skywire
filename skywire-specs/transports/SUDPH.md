# SUDPH - Skywire UDP Hole-Punch

SUDPH (Skywire UDP Hole-Punch) is a UDP-based transport that uses hole-punching techniques to establish direct peer-to-peer connections between visors behind NAT or firewalls.

## Overview

SUDPH enables direct connections between visors that would otherwise be unreachable due to NAT. It uses an address-resolver server as a rendezvous point to coordinate the hole-punching process, then establishes a direct UDP connection using the KCP protocol for reliable delivery.

## Architecture

```
                          ┌──────────────────┐
                          │ Address Resolver │
                          │   (Rendezvous)   │
                          └────────┬─────────┘
                                   │
              ┌────────────────────┼────────────────────┐
              │ UDP (register)     │     UDP (register) │
              ▼                    │                    ▼
       ┌────────────┐              │             ┌────────────┐
       │   NAT A    │              │             │   NAT B    │
       └──────┬─────┘              │             └──────┬─────┘
              │                    │                    │
       ┌──────┴─────┐   UDP hole-punch           ┌──────┴─────┐
       │  Visor A   │◄─────────────────────────►│  Visor B   │
       │  (port AP) │      Direct connection     │  (port BP) │
       └────────────┘                            └────────────┘
```

## How UDP Hole-Punching Works

### The NAT Problem

When a visor is behind NAT, it has a private IP address that is not directly reachable from the internet. NAT creates temporary mappings when the visor sends outbound packets, but these mappings aren't usable for incoming connections from arbitrary hosts.

### The Solution

1. Both visors (`A` and `B`) register with the address-resolver server (`S`)
2. When `A` wants to connect to `B`:
   - `A` asks `S` for `B`'s public address
   - `S` notifies `B` that `A` wants to connect and provides `A`'s address
3. Both visors send UDP packets to each other simultaneously
4. These packets "punch holes" in their respective NATs by creating outbound mappings
5. Once mappings exist on both sides, direct communication is possible

### Same LAN Optimization

When both visors share the same public IP (same LAN), SUDPH:
1. Detects matching public IPs during address resolution
2. Retrieves the remote visor's local/LAN addresses
3. Attempts hole-punch to LAN addresses first for lower latency

## Connection Flow

### Binding (Server-side)

1. Visor listens on a UDP port (configurable, or auto-increments if in use)
2. Creates a `PacketFilter` to multiplex the UDP socket between:
   - Address-resolver connection (for registration and signaling)
   - Peer connections (for hole-punched transports)
3. Performs Noise handshake with address-resolver over UDP
4. Sends binding information (port, local addresses)
5. Waits for incoming dial requests via `addrCh` channel

### Dialing (Client-side)

1. Dialing visor calls `GET /resolve/sudph/{pk}` to get target's address
2. Address-resolver notifies the target visor of the incoming dial request
3. Both visors send "holepunch" packets to each other's addresses
4. Dialing visor creates a KCP connection over the hole-punched UDP path
5. Transport handshake completes the connection

## Key Components

### Packet Filter (`pfilter`)

The `pfilter.PacketFilter` multiplexes a single UDP socket for multiple logical connections:

| Priority | Connection Type | Purpose |
|----------|-----------------|---------|
| 3 | `visorsConn` | Accepts incoming hole-punch connections |
| 2 | `dialConn` | Used for outgoing dial attempts |

Higher priority connections are checked first when routing incoming packets.

### KCP Protocol

[KCP](https://github.com/xtaci/kcp-go) provides reliable, ordered delivery over UDP:

- ARQ (Automatic Repeat reQuest) for reliability
- Configurable congestion control
- Lower latency than TCP in high-loss networks

### Hole-Punch Message

A constant message `"holepunch"` is sent in initial UDP packets to establish NAT mappings:

```go
const holePunchMessage = "holepunch"
```

## Address Resolver API

### UDP Binding (Noise Handshake)

Visors bind via UDP with a Noise protocol handshake:

1. Visor initiates Noise handshake with address-resolver
2. After handshake, visor sends binding data:
   ```json
   {
       "port": "8080",
       "addresses": ["192.168.1.100", "10.0.0.5"]
   }
   ```
3. Address-resolver stores the visor's public address and local addresses

### GET /resolve/sudph/{pk}

Resolves a public key to network address. Also triggers notification to the target visor.

**Response:**
```json
{
    "address": "203.0.113.50:8080",
    "port": "8080",
    "addresses": ["192.168.1.100", "10.0.0.5"]
}
```

### GET /security/nonces/{pk}

Returns the next expected nonce for HTTP authentication.

## Timing Parameters

| Parameter | Value | Description |
|-----------|-------|-------------|
| Dial timeout | 30 seconds | Maximum time to wait for hole-punch to succeed |
| Hole-punch retries | Continuous | Keeps trying until timeout |

## Configuration

```json
{
    "sudph": {
        "port": 8080,
        "address_resolver": "https://ar.skywire.dev"
    }
}
```

| Field | Description |
|-------|-------------|
| `port` | Local UDP port to listen on (0 = auto-select) |
| `address_resolver` | URL of the address-resolver service |

## Advantages

- **NAT traversal**: Works through most NAT types without port forwarding
- **Direct connection**: After hole-punch, traffic flows directly between peers
- **Low latency**: UDP + KCP provides lower latency than TCP
- **Same-LAN optimization**: Uses local addresses when visors share public IP

## Limitations

- **Symmetric NAT**: May fail with symmetric NAT that assigns different ports per destination
- **UDP blocking**: Some networks block UDP traffic
- **Requires rendezvous**: Depends on address-resolver for initial coordination
- **Connection time**: Hole-punching adds setup latency

## NAT Type Compatibility

| NAT Type | Success Rate |
|----------|--------------|
| Full Cone | High |
| Restricted Cone | High |
| Port Restricted Cone | Medium-High |
| Symmetric | Low |

## Code References

- Implementation: `pkg/transport/network/sudph.go`
- Packet filter: `internal/packetfilter/`
- Address resolver client: `pkg/transport/network/addrresolver/`
- Transport type constant: `pkg/transport/types/types.go:13`

## Useful Links

- [UDP Hole Punching (Bford)](https://bford.info/pub/net/p2pnat/index.html) - Seminal paper on NAT traversal
- [KCP Protocol](https://github.com/xtaci/kcp-go) - Reliable UDP implementation

## See Also

- [STCPR](STCPR.md) - TCP transport (no hole-punching, requires port forwarding)
- [STCP](STCP.md) - Direct TCP with local PK table
- [DMSG](DMSG.md) - Overlay network fallback
