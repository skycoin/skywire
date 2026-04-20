# Packets

Packets are the data units transmitted over Skywire transports and routes. Every packet has a fixed 7-byte header followed by a variable-length payload.

## Packet Format

```
| type (1 byte) | route ID (4 bytes BE) | payload size (2 bytes BE) | payload |
| offset 0      | offset 1              | offset 5                  | offset 7 |
```

- `PacketHeaderSize` = 7 bytes
- Maximum payload size: 65535 bytes (uint16 max)
- Route ID 0 is reserved for transport-level frames (not routed)

## Packet Types

| Type | Value | Payload | Description |
|---|---|---|---|
| `DataPacket` | 0 | Application data | Carries route group payload. When CapMux is active, a 4-byte sequence number is prepended. |
| `ClosePacket` | 1 | CloseCode (1 byte) | Closes the route group. CloseCode 0 = `CloseRequested`. |
| `KeepAlivePacket` | 2 | Empty | Refreshes routing rule TTL at each hop. Intermediary nodes forward it; edge nodes consume it. |
| `HandshakePacket` | 3 | Encryption flag (1 byte) + capabilities (2 bytes LE) | Initiates Noise protocol handshake and negotiates capabilities. |
| `PingPacket` | 4 | Timestamp (8 bytes BE) + throughput (8 bytes BE) | Route-level latency measurement. Requires an established route. |
| `PongPacket` | 5 | Timestamp (8 bytes BE) | Route-level pong response. Echoes the timestamp from PingPacket. |
| `ErrorPacket` | 6 | Error message (variable) | Error notification to the route group. |
| `SACKPacket` | 7 | Last contiguous seq (4 bytes BE) + bitmap (8 bytes BE) | Selective acknowledgment for CapSACK retransmission. |
| `TransportPingPacket` | 8 | Timestamp (8 bytes BE, unix nano) | Transport-level latency measurement. Route ID = 0. Intercepted before routing. |
| `TransportPongPacket` | 9 | Timestamp (8 bytes BE, echoed) | Transport-level pong response. Route ID = 0. Intercepted before routing. |

## Handshake Capabilities

The `HandshakePacket` payload byte 0 is the encryption flag (1 = encrypt, 0 = plaintext). Bytes 1-2 are a little-endian capability bitmap:

| Bit | Flag | Description |
|---|---|---|
| 0 | `CapMux` | Route multiplexing — DataPackets carry a 4-byte sequence number prefix |
| 1 | `CapSACK` | Selective acknowledgment — enables SACKPacket retransmission |

Capabilities are negotiated: a feature is enabled only when both peers advertise it.

## Packet Flow

### Edge Visor (Source)

1. Application writes data to the route group
2. If CapMux: prepend 4-byte sequence number
3. Construct `DataPacket` with the forward route ID from the ForwardRule
4. Write packet to the transport specified in the ForwardRule

### Intermediary Visor

1. Read packet from incoming transport
2. Look up route ID in routing table → get IntermediaryForwardRule
3. Reconstruct packet with the next-hop route ID
4. Write to the next transport

The intermediary never decrypts the payload (Noise encryption is end-to-end between edges).

### Edge Visor (Destination)

1. Read packet from transport
2. Look up route ID → get ConsumeRule
3. Deliver packet to the local route group
4. Route group decrypts via Noise and delivers to the application

## Transport-Level vs Route-Level Frames

Packets with route ID = 0 are transport-level frames:
- `TransportPingPacket` and `TransportPongPacket` are intercepted by `ManagedTransport.readLoop()` before reaching the router
- They measure transport latency without requiring route setup
- All other packet types require a valid route ID > 0 and are dispatched by the router
