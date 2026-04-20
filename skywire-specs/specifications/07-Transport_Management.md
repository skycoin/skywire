# Transport Management

The *Transport Manager* (`pkg/transport/manager.go`) is responsible for creating, accepting, managing, and logging all transports for a visor.

## Responsibilities

1. **Network client initialization** — creates transport-type-specific network clients (STCPR, SUDPH, STCP, DMSG) based on the visor's configuration
2. **Transport lifecycle** — dial outbound transports, accept inbound transports, serve read loops, close and deregister on shutdown
3. **Transport Discovery registration** — registers transports with the TPD on creation and re-registers every 90 seconds with updated bandwidth/latency data
4. **Bandwidth and latency tracking** — each managed transport tracks cumulative bytes sent/received and RTT via transport-level ping
5. **Label management** — assigns transport labels (`skycoin`, `automatic`, `user`) and propagates the initiator's label to the responder during settlement

## Transport Creation

### `SaveTransport(ctx, remotePK, netType, label)`

Creates a transport to a remote visor:

1. Checks if a transport to the remote PK of the same type already exists
2. If an existing transport exists and has active routes (checked via `RouteChecker`), the creation is rejected to protect in-flight traffic
3. Dials the remote visor using the type-specific network client
4. Performs the settlement handshake (exchange Entry data, agree on Transport ID)
5. Starts the managed transport's `Serve` loop (readLoop, logLoop, pingLoop)
6. Registers the `SignedEntry` with the Transport Discovery

### Transport Acceptance

The manager runs accept loops for each initialized network client. When a remote visor initiates a transport:

1. The accept loop receives the incoming connection
2. Performs the settlement handshake
3. Adopts the initiator's transport label
4. Starts the managed transport's `Serve` loop

## Re-Registration

Every 90 seconds (`transportReRegisterInterval`), the manager re-registers all active transports with the Transport Discovery:

1. Collects `SignedEntry` for each non-closed transport, including:
   - Current latency stats (from transport-level ping)
   - Cumulative bandwidth (sent + received bytes)
   - Visor version string
2. Calls `DiscoveryClient.RegisterTransports(entries...)`
3. Optionally syncs all TPD data back for local route calculation (`sync_tpd_data` config)
4. Records a heartbeat for integrated uptime tracking

## Batch Deregistration

When transports close, their IDs are queued for deferred batch deletion from the TPD instead of making individual HTTP calls. The batch deletion loop runs periodically and sends a single `DeleteTransports` request.

## Configuration

```json
{
  "transport": {
    "discovery": "http://tpd.skywire.skycoin.com",
    "discovery_dmsg": "dmsg://<pk>:80",
    "address_resolver": "http://ar.skywire.skycoin.com",
    "address_resolver_dmsg": "dmsg://<pk>:80",
    "public_autoconnect": true,
    "transport_setup": ["<tps-pk-1>", "<tps-pk-2>"],
    "stcpr_port": 0,
    "sudph_port": 0,
    "log_store": {
      "type": "file",
      "location": "./local/transport_logs",
      "rotation_interval": "168h0m0s"
    }
  }
}
```

## Log Store

Transport bandwidth logs are persisted to CSV files:
- Format: `transport_id, recv_bytes, sent_bytes, timestamp`
- Files named by date: `YYYY-MM-DD.csv`
- Auto-rotated daily
- Retention configurable via `rotation_interval`
