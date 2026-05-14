# Transport-Level Ping

Transport-level ping measures latency directly on the transport without requiring a route to be established. It uses dedicated packet types (`TransportPingPacket` type 8, `TransportPongPacket` type 9) with route ID 0 that are intercepted at the transport layer before reaching the router.

## Purpose

Prior to transport-level ping, latency measurement required the Route Setup Node to establish a temporary route, send route-level pings, and tear down the route. This consumed ~90% of RSN traffic — every transport creation triggered an RSN route setup just for latency measurement.

Transport-level ping eliminates RSN involvement entirely. The transport measures its own latency using frames that flow directly over the existing connection.

## Packet Format

Both ping and pong use the standard 7-byte routing packet header with route ID = 0:

```
| type (1) | route ID = 0 (4) | payload size = 8 (2) | timestamp (8) |
```

- **TransportPingPacket (type 8):** payload is a unix nano timestamp (int64, big-endian)
- **TransportPongPacket (type 9):** payload echoes the timestamp from the ping

Total frame size: 15 bytes (7 header + 8 payload).

## Flow

1. `ManagedTransport.pingLoop()` starts when the transport is served
2. Waits for the underlying connection to be ready (`transportCh`)
3. Sends the first ping immediately
4. Sends subsequent pings every 30 seconds (`transportPingInterval`)
5. `readLoop()` intercepts packets with route ID 0 and type 8/9 before forwarding to the router:
   - **TransportPingPacket:** immediately responds with a TransportPongPacket echoing the timestamp
   - **TransportPongPacket:** computes RTT as `(now - sentAt) / 1e6` milliseconds, calls `SetLatency(rttMs)`

## Backward Compatibility

Old visors that don't recognize types 8/9 will forward them to the router, which hits the default case and logs a warning. The packet is dropped harmlessly.

To handle this during transition, a 90-second grace period applies:
1. The `pingLoop` sends pings normally
2. After 90 seconds, if `GetLatency() == 0` (no pong received), the visor falls back to RSN-based `MeasureTransportLatency` via the `LatencyFallbackCallback`
3. Once a pong is received (latency > 0), the fallback is never triggered

## Latency Storage

Latency measurements are stored on the `ManagedTransport`:

```go
type LatencyStats struct {
    Min float64  // Minimum observed latency (ms)
    Max float64  // Maximum observed latency (ms)
    Avg float64  // Average latency (ms)
}
```

`SetLatency(rttMs)` updates the average and maintains min/max. The transport manager no longer pushes latency to the Transport Discovery on re-registration; instead, the visor's local telemetry store samples latency every minute and publishes rollups on the visor's CXO feed (see §07 Transport Management — *Visor-Local Telemetry Store*).

## Bandwidth Tracking

Bandwidth is tracked separately from latency, at the transport layer:
- `logSent(bytes)` on every `WritePacket`
- `logRecv(bytes)` on every `readPacket`
- Only payload bytes are counted (7-byte header excluded)
- Persisted to CSV files every 3 seconds via `logLoop`
- Sampled into the visor's local telemetry store every minute as cumulative `sent_bytes` / `recv_bytes`; daily deltas are computed from a per-transport day-start baseline

The Transport Discovery acquires bandwidth and latency history by subscribing to each visor's CXO feed; it no longer receives this data via push.

Transport-level ping/pong frames contribute minimally to bandwidth (~30 bytes/minute per transport).
