# Transport

A *Transport* represents a bidirectional, encrypted line of communication between two *Visors* (called *Transport Edges*).

Each transport is identified by a unique *Transport ID* (UUID) and has a *Transport Type* that identifies the underlying protocol implementation.

## Transport Entry

A transport is represented in the Transport Discovery by an `Entry`:

```go
// Entry is the unsigned representation of a Transport.
type Entry struct {
    ID        uuid.UUID      `json:"t_id"`
    Edges     [2]cipher.PubKey `json:"edges"`
    Type      types.Type     `json:"type"`
    Label     Label          `json:"label"`
    Latency   float64        `json:"latency_ms,omitempty"`
    Bandwidth uint64         `json:"bandwidth,omitempty"`
}
```

JSON representation:

```json
{
    "t_id": "e1808c31-6b23-d1d6-119c-ad1795238ff0",
    "edges": [
        "031d796272349d597d6d3130497ccd11cf8af12c7d186b1726358abfb49edad0c1",
        "03bd9724f335c5eb5a1011e7862d4af28488102c8edffc84585cf0826ac4864b38"
    ],
    "type": "stcpr",
    "label": "automatic",
    "latency_ms": 12.5,
    "bandwidth": 1234567
}
```

### Transport ID

The Transport ID is derived deterministically from the two edge public keys and the transport type. The edges are sorted in ascending order to ensure both sides compute the same ID.

### Transport Types

| Type | Protocol | Address Resolution | NAT Traversal |
|---|---|---|---|
| `stcpr` | TCP | Address Resolver service | No (requires public IP or port forwarding) |
| `sudph` | UDP | Address Resolver service | Yes (UDP hole-punching) |
| `stcp` | TCP | Local PK-to-IP table | No |
| `dmsg` | DMSG relay | DMSG Discovery | Yes (relayed through DMSG server) |

### Transport Labels

Labels identify the origin of a transport and control access via the Transport Setup Node:

| Label | Created by | TPS can list | TPS can remove |
|---|---|---|---|
| `skycoin` | Transport Setup Node | Yes | Yes (if no active route) |
| `automatic` | Public autoconnect | Yes | Yes (if no active route) |
| `user` | Manual CLI / hypervisor UI | No | No |

## Signed Entry

Transports are registered in the Transport Discovery as `SignedEntry`:

```go
type SignedEntry struct {
    Entry      *Entry         `json:"entry"`
    Signatures [2]cipher.Sig  `json:"signatures"`
    Registered int64          `json:"registered,omitempty"`
    Latency    *LatencyData   `json:"latency,omitempty"`
    Bandwidth  *BandwidthData `json:"bandwidth,omitempty"`
    Version    string         `json:"version,omitempty"`
}

type LatencyData struct {
    Min int64 `json:"min"` // microseconds
    Max int64 `json:"max"`
    Avg int64 `json:"avg"`
}

type BandwidthData struct {
    SentBytes uint64 `json:"sent_bytes"`
    RecvBytes uint64 `json:"recv_bytes"`
}
```

The `Latency` and `Bandwidth` fields are updated on each transport re-registration (every 90 seconds). Latency is measured by transport-level ping; bandwidth is tracked per-packet by the managed transport.

## Settlement Handshake

When a transport is established between two visors, a settlement handshake occurs:

1. The initiator dials the remote visor using the transport-type-specific mechanism (TCP connect, UDP hole-punch, or DMSG relay)
2. Both sides exchange signed `Entry` data including the agreed transport ID, type, and edges
3. The responder adopts the initiator's label so both ends agree on the transport's origin
4. The initiator registers the `SignedEntry` with the Transport Discovery

## Managed Transport

In the visor, each transport is wrapped in a `ManagedTransport` (`pkg/transport/managed_transport.go`) which provides:

- **Read loop** — reads packets from the underlying connection, intercepts transport-level ping/pong (route ID 0), forwards routing packets to the router
- **Log loop** — tracks sent/received bytes per packet, persists to CSV every 3 seconds
- **Ping loop** — sends transport-level ping every 30 seconds, measures RTT
- **Latency stats** — min/max/avg latency in milliseconds
- **Bandwidth stats** — cumulative sent/received bytes

The managed transport's lifecycle:

1. `Serve(readCh)` starts 4 goroutines: readLoop, logLoop, pingLoop, and the main serve loop
2. Packets from readLoop are sent to the router via `readCh`
3. On close, the transport is deregistered from the Transport Discovery

## Transport Interface

```go
// Transport is the underlying network connection.
type Transport interface {
    Read(p []byte) (n int, err error)
    Write(p []byte) (n int, err error)
    Close() error
    LocalPK() cipher.PubKey
    RemotePK() cipher.PubKey
    Type() types.Type
    SetDeadline(t time.Time) error
    SetReadDeadline(t time.Time) error
    SetWriteDeadline(t time.Time) error
}
```

## Network Client

Each transport type has a `network.Client` implementation that handles dialing and accepting connections:

```go
type Client interface {
    Dial(ctx context.Context, remote cipher.PubKey, port uint16) (Transport, error)
    Listen(port uint16) (Listener, error)
    PK() cipher.PubKey
    Type() types.Type
    Close() error
}
```
