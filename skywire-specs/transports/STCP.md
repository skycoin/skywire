# STCP - Skywire TCP (Direct)

STCP (Skywire TCP) is a TCP-based transport that uses a locally configured PK-to-IP mapping table for address resolution. It is designed for controlled environments where visor addresses are known in advance.

## Overview

Unlike STCPR which relies on an external address-resolver service, STCP uses a static configuration table that maps public keys to IP addresses. This makes it ideal for:

- Local/private networks
- Development and testing environments
- Networks with static IP assignments
- Scenarios where external service dependencies are undesirable

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         Visor A                                  │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                    PK Table                              │    │
│  │  ┌──────────────────────┬──────────────────────────┐    │    │
│  │  │ Public Key           │ Address                   │    │    │
│  │  ├──────────────────────┼──────────────────────────┤    │    │
│  │  │ 02abc...             │ 192.168.1.100:7777       │    │    │
│  │  │ 03def...             │ 192.168.1.101:7777       │    │    │
│  │  │ 04ghi...             │ 10.0.0.50:7777           │    │    │
│  │  └──────────────────────┴──────────────────────────┘    │    │
│  └─────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────┘
                              │
                              │ Direct TCP
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                         Visor B                                  │
│                    (192.168.1.100:7777)                         │
└─────────────────────────────────────────────────────────────────┘
```

## Connection Flow

### Server-side (Listening)

1. Visor reads `listening_address` from configuration
2. Starts TCP listener on the configured address
3. Accepts incoming connections
4. Performs transport handshake to authenticate remote visor

### Client-side (Dialing)

1. Visor looks up target public key in the PK table
2. If found, dials the corresponding IP:port directly via TCP
3. Performs transport handshake
4. Connection established

### Address Resolution

```go
// PKTable interface
type PKTable interface {
    Addr(pk cipher.PubKey) (string, bool)
}
```

If the public key is not found in the table, the dial fails with `ErrStcpEntryNotFound`.

## Configuration

In the visor config file, STCP is configured under the `"skywire-tcp"` key:

```json
{
    "skywire-tcp": {
        "pk_table": {
            "02abc123def456...": "192.168.1.100:7777",
            "03def456abc123...": "192.168.1.101:7777",
            "04789ghi012jkl...": "10.0.0.50:8888"
        },
        "listening_address": ":7777"
    }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `pk_table` | object | Map of public key (66-char hex) to IP:port address |
| `listening_address` | string | Local address to listen on (e.g., `:7777` or `0.0.0.0:7777`) |

### PK Table Format

Each entry in the PK table maps a visor's public key to its network address:

```json
{
    "<66-char-hex-public-key>": "<ip>:<port>"
}
```

**Example:**
```json
{
    "02a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2": "192.168.1.100:7777",
    "03f6e5d4c3b2a1f6e5d4c3b2a1f6e5d4c3b2a1f6e5d4c3b2a1f6e5d4c3b2a1f6e5": "10.0.0.50:7777"
}
```

## Transport Handshake

After TCP connection is established:

1. Initiator sends Noise protocol handshake initiation
2. Responder verifies initiator's public key
3. Session keys are derived for encrypted communication
4. Transport is ready for use

## Error Handling

| Error | Cause | Resolution |
|-------|-------|------------|
| `ErrStcpEntryNotFound` | Public key not in PK table | Add entry to `pk_table` configuration |
| Connection refused | Target not listening | Ensure target visor is running and listening |
| Handshake failed | Key mismatch or network issue | Verify correct PK in table, check network |

## Use Cases

### Local Development

```json
{
    "skywire-tcp": {
        "pk_table": {
            "02dev1a2b3c4d5e6f...": "127.0.0.1:7778",
            "02dev2f6e5d4c3b2a...": "127.0.0.1:7779"
        },
        "listening_address": ":7777"
    }
}
```

### Private Network Deployment

```json
{
    "skywire-tcp": {
        "pk_table": {
            "02server1abc123...": "10.0.0.2:7777",
            "02server2def456...": "10.0.0.3:7777",
            "02server3ghi789...": "10.0.0.4:7777"
        },
        "listening_address": "10.0.0.1:7777"
    }
}
```

## Advantages

- **No external dependencies**: Works without address-resolver service
- **Predictable**: Static configuration means deterministic behavior
- **Simple**: Easy to understand and debug
- **Low latency**: Direct TCP with no lookup overhead
- **Offline capable**: Works in isolated networks

## Limitations

- **Static configuration**: Requires manual updates when addresses change
- **No NAT traversal**: Both visors must be directly reachable
- **Scalability**: PK table must be maintained across all visors
- **No dynamic discovery**: New visors require configuration updates

## Comparison with STCPR

| Feature | STCP | STCPR |
|---------|------|-------|
| Address resolution | Local PK table | Address-resolver service |
| Dynamic IPs | Not supported | Supported (re-registration) |
| External dependencies | None | Requires address-resolver |
| Configuration | Per-visor PK table | Address-resolver URL only |
| Best for | Static/private networks | Dynamic/public networks |

## Code References

- Implementation: `pkg/transport/network/stcp.go`
- PK Table interface: `pkg/transport/network/stcp/table.go`
- Transport type constant: `pkg/transport/types/types.go:15`

## See Also

- [STCPR](STCPR.md) - TCP with address-resolver (dynamic resolution)
- [SUDPH](SUDPH.md) - UDP hole-punching (NAT traversal)
- [DMSG](DMSG.md) - Overlay network transport
