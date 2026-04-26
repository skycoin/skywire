# Transport Management

The *Transport Manager* (`pkg/transport/manager.go`) is responsible for creating, accepting, managing, and logging all transports for a visor.

## Responsibilities

1. **Network client initialization** — creates transport-type-specific network clients (STCPR, SUDPH, STCP, DMSG) based on the visor's configuration
2. **Transport lifecycle** — dial outbound transports, accept inbound transports, serve read loops, close and deregister on shutdown
3. **Transport Discovery registration** — registers transports with the TPD on creation and re-registers every 90 seconds. Re-registration carries the live transport set only (edges, type, label) — bandwidth and latency are no longer pushed; see §Visor-Local Telemetry Store
4. **Bandwidth and latency measurement** — each managed transport tracks cumulative bytes sent/received and RTT via transport-level ping; samples are written to the local telemetry store
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

Every 90 seconds (`transportReRegisterInterval`), the manager re-registers all active transports with the Transport Discovery via the v3 bare-entry endpoint (`POST /v3/transports/`):

1. Collects `transport.Entry` for each non-closed transport (id, edges, type, label)
2. Calls `DiscoveryClient.RegisterTransportsV3(version, entries...)`
3. The TPD records a heartbeat for the registering visor on each call

Bandwidth and latency are not part of the re-registration body. They are sampled into the visor-local telemetry store (see §Visor-Local Telemetry Store) and reach the TPD by it subscribing to each visor's CXO feed.

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

The CSV log store is the historical raw-event log. The structured rollup store described below is the source of truth for queries and remote aggregation.

## Visor-Local Telemetry Store

The visor maintains a local bbolt database (`<local_path>/stats.db`) recording per-transport bandwidth/latency rollups, three-tier uptime intervals, and per-service uptime intervals. This is the source of truth for everything the Transport Discovery, the Uptime Tracker, and the Service Discovery used to track centrally; those services now act as aggregators that pull from each visor.

### Schema

Four buckets under `stats.db`:

| Bucket | Sub-bucket | Key | Value |
|---|---|---|---|
| `meta` | — | `schema_version`, `created_at` | Schema metadata only (the CXO feed identity is the visor's existing keypair — no separate key is generated) |
| `transports` | — | transport ID (UUID, 16 bytes) | `TransportRecord` JSON: `{id, edges, type, label, first_seen, last_seen, current, daily}` |
| `tiers` | `process` \| `dmsg` \| `skynet` | `<YYYY-MM-DD>` (UTC) | 36-byte raw bitmap (288 bits, one per 5-minute slot) |
| `services` | service slug (`vpn-server`, `skysocks`, `skychat`, `visor`, …) | `<YYYY-MM-DD>` (UTC) | 36-byte raw bitmap |

`TransportRecord.current` is overwritten on every sample with the live snapshot (`sent_bytes`, `recv_bytes`, latency min/max/avg). `TransportRecord.daily` is append-only, one entry per UTC day, holding the *delta* sent/recv since the day's start baseline plus accumulated latency stats.

The bitmap encoding for `tiers` and `services` is bit-identical to the format the Transport Discovery already uses for integrated uptime tracking (`pkg/transport-discovery/store/redis_uptime.go`): each of the 288 5-minute slots in a UTC day maps to one bit; bit set = the tier/service was online during at least one sample within the slot. Renderers convert the 36 raw bytes to a 288-char ASCII string (`.` = set, ` ` = unset) at the HTTP / display boundary.

### Sampling

A sampler ticks every `StatsSampleInterval` (default `1m`). On each tick:

- For each live transport: capture `tp.GetBandwidth()` and `tp.GetLatencyStats()`, update today's daily row using an in-memory day-start baseline, overwrite the `current` snapshot.
- For each tier currently online and each registered service currently online: set the bit corresponding to the current 5-minute slot in `<bucket>/<name>/<today>`.

At UTC-midnight transition the day's transport row is sealed (baseline reset to the current cumulative counters) and a new bitmap key is created for each tier/service that's still online.

### Three-Tier Uptime

| Tier | Online when |
|---|---|
| `process` | Visor is running |
| `dmsg` | The visor's DMSG client is connected (`dmsgC.Ready()` has fired and no reconnect is in progress) |
| `skynet` | The visor has ≥2 live transports |

Tier state is checked on each sampler tick; `init_dmsg.go` and `transport/manager.go` expose state probes the sampler queries directly (no event bus).

A consumer that wants the strict "really online" view can compute `process AND dmsg` (or `process AND dmsg AND skynet`) by AND-ing the corresponding 36-byte bitmaps for the same date.

### Per-Service Uptime

For each app registered with the Service Discovery (via `pkg/app/appdisc/discovery_manager.go`), the sampler queries the running `serviceUpdater` set on each tick and sets the slot bit for any currently-running service. The slug matches the service name registered with the Service Discovery (`vpn-server`, `skysocks`, …). The visor itself is tracked under the slug `visor` while the visor process is alive.

### Retention

A retention sweep runs at each UTC-midnight transition:
- `transports.daily` is trimmed to the last `StatsRetentionDays` entries (default `30`)
- Closed-transport records with empty `daily` and no recent `current` are deleted entirely
- `tiers/<tier>/<date>` and `services/<svc>/<date>` keys with `date` older than `StatsRetentionDays` are deleted

### Query Surface

The store is exposed read-only via two channels:

- **HTTP-over-DMSG** on the existing log server at `dmsg://<pk>:80`, behind the survey-whitelist auth (`authRoute` group). The whitelist on these endpoints is a DoS bound on arbitrary historical ranges, not a confidentiality measure — the same data flows openly on the CXO feed.
  - `GET /stats/transports` — live snapshot of all current transports
  - `GET /stats/transports/history?since=<RFC3339>&until=<RFC3339>&id=<uuid>` — daily rollups within range; `id` filters single transport
  - `GET /stats/uptime?since=<RFC3339>` — tier bitmaps rendered as 288-char ASCII per day
  - `GET /stats/services?since=<RFC3339>` — per-service bitmaps in the same format
- **CXO publisher** (see §CXO Publisher) — the visor publishes a rolling window of the same data on its feed; subscribers (notably the Transport Discovery) get push updates without polling.

### CXO Publisher

The visor runs a `pkg/cxo/publisher` instance using its existing keypair: `publisher.New(dmsgC, v.conf.SK, conf)`. The feed PK is therefore identical to the visor's PK — no separate identity is generated, and aggregators (TPD, etc.) already know it from the transport registry. The CXO feed is open to any subscriber that knows the visor's PK; this matches the existing data sensitivity (TPD's `/metric` and the DHT `tp` entries are also public).

The publish loop ticks every `StatsSampleInterval` and writes:

| Key | Value |
|---|---|
| `transports/<id>/current` | Live snapshot JSON |
| `transports/<id>/<date>` | Daily rollup JSON |
| `tiers/<tier>/<date>` | 36 raw bytes (288-bit bitmap) |
| `services/<svc>/<date>` | 36 raw bytes (288-bit bitmap) |

Bitmaps travel as raw bytes on the wire — 8× smaller than the 288-char ASCII rendering — and subscribers convert at egress. Keys outside the `CXOPublishWindow` (default `7d`) are deleted at each tick. The publisher's effective window is rolling; the bbolt store retains the full `StatsRetentionDays`.

### Configuration

```json
{
  "stats": {
    "path": "<local_path>/stats.db",
    "retention_days": 30,
    "sample_interval": "1m",
    "cxo_publish_window": 7,
    "disabled": false
  }
}
```
