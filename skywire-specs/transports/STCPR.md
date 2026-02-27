# STCPR - Skywire TCP with Address Resolver

STCPR (Skywire TCP Resolved) is a TCP-based transport that uses the address-resolver service to discover remote visor addresses by their public keys.

## Overview

Unlike STCP which requires a local PK-to-IP mapping table, STCPR dynamically resolves visor addresses through a centralized address-resolver service. This allows visors to connect without prior knowledge of each other's IP addresses.

## Architecture

```
┌─────────┐                    ┌──────────────────┐                    ┌─────────┐
│ Visor A │◄──── TCP ─────────►│ Address Resolver │◄──── TCP ─────────►│ Visor B │
└─────────┘                    └──────────────────┘                    └─────────┘
     │                                                                       │
     │                         Direct TCP Connection                         │
     └───────────────────────────────────────────────────────────────────────┘
```

## Connection Flow

### Binding (Server-side)

1. Visor starts listening on a TCP port (configurable, or auto-increments if port is in use)
2. Visor registers with address-resolver via `POST /bind/stcpr` with:
   - Its public key (authenticated via `httpauth`)
   - The port it's listening on
   - Local IP addresses
3. Visor re-registers every **90 seconds** to keep the entry alive in the address-resolver

### Dialing (Client-side)

1. Dialing visor calls `GET /resolve/stcpr/{pk}` on address-resolver to get the target visor's address
2. Address-resolver returns the IP:port of the target visor
3. Dialing visor establishes a direct TCP connection to the resolved address
4. Transport handshake is performed over the TCP connection

## Address Resolver API

### POST /bind/stcpr

Binds a visor's public key to its network address. Requires PK authentication.

**Request:**
```json
{
    "port": "7777",
    "addresses": ["192.168.1.100", "10.0.0.5"]
}
```

**Headers:**
- `SW-Public`: Visor's public key
- `SW-Nonce`: Security nonce
- `SW-Sig`: Request signature

### GET /resolve/stcpr/{pk}

Resolves a public key to a network address. Requires PK authentication.

**Response:**
```json
{
    "address": "203.0.113.50:7777",
    "port": "7777",
    "addresses": ["192.168.1.100", "10.0.0.5"]
}
```

### GET /security/nonces/{pk}

Returns the next expected nonce for a public key. Used by `httpauth` middleware.

## Re-registration

To maintain presence in the address-resolver, visors re-register periodically:

| Parameter | Value |
|-----------|-------|
| Re-registration interval | 90 seconds |
| Entry TTL (server-side) | ~2-3 minutes |

If a visor fails to re-register, its entry expires and it becomes unreachable via STCPR.

## Transport Handshake

After TCP connection is established, a Noise protocol handshake authenticates both parties:

1. Initiator sends handshake initiation with its public key
2. Responder verifies and responds
3. Both parties derive session keys for encrypted communication

## Configuration

```json
{
    "stcpr": {
        "port": 7777,
        "address_resolver": "https://ar.skywire.dev"
    }
}
```

| Field | Description |
|-------|-------------|
| `port` | Local TCP port to listen on (0 = auto-select) |
| `address_resolver` | URL of the address-resolver service |

## Advantages

- **No static configuration**: Visors don't need to know each other's IPs in advance
- **Dynamic IP support**: Works with changing IP addresses (re-registration updates the resolver)
- **NAT-friendly**: Works as long as TCP connections can be established (may require port forwarding)

## Limitations

- **Centralized dependency**: Requires address-resolver service to be available
- **NAT traversal**: Does not perform hole-punching; requires port forwarding for visors behind NAT
- **TCP overhead**: Higher latency than UDP-based transports

## Code References

- Implementation: `pkg/transport/network/stcpr.go`
- Address resolver client: `pkg/transport/network/addrresolver/`
- Transport type constant: `pkg/transport/types/types.go:10`

## See Also

- [SUDPH](SUDPH.md) - UDP hole-punching transport (better NAT traversal)
- [STCP](STCP.md) - Direct TCP with local PK table
- [DMSG](DMSG.md) - Overlay network transport
