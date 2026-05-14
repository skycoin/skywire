# Transport Management

The *Transport Manager* is responsible for creating, accepting, managing, and logging all transports for a visor.

## Responsibilities

1. **Network client initialization** — creates transport-type-specific network clients (STCPR, SUDPH, STCP, DMSG) based on the visor's configuration
2. **Transport lifecycle** — dial outbound transports, accept inbound transports, serve read loops, close and deregister on shutdown
3. **Transport Discovery registration** — registers transports with the TPD on creation and re-publishes the live transport set every 90 seconds. The published payload is the bare entry (edges, type, label); bandwidth and latency travel on the visor's telemetry feed, not this channel (see §Visor-Local Telemetry Store)
4. **Bandwidth and latency measurement** — each managed transport tracks cumulative bytes sent/received and RTT via transport-level ping; samples are written to the local telemetry store
5. **Label management** — assigns transport labels (`skycoin`, `automatic`, `user`) and propagates the initiator's label to the responder during settlement

## Transport Creation

### Outbound Creation

To create a transport to a remote visor the manager SHALL:

1. Reject the creation if a transport of the same type to the remote PK already exists AND that existing transport currently carries active routes.
2. Dial the remote visor using the network client for the requested type.
3. Perform the settlement handshake — exchange the proposed Entry and agree on the Transport ID (see §04 Transport Discovery).
4. Start the managed transport's serve loop (read, log, and ping subloops).
5. Register the resulting `SignedEntry` with the Transport Discovery.

### Transport Acceptance

The manager runs accept loops for each initialized network client. When a remote visor initiates a transport:

1. The accept loop receives the incoming connection
2. Performs the settlement handshake
3. Adopts the initiator's transport label
4. Starts the managed transport's `Serve` loop

## Re-Registration

Every 90 seconds the manager SHALL re-publish its live transport set to the Transport Discovery. The published payload SHALL contain only the bare `transport.Entry` fields (id, edges, type, label) for each non-closed transport; this is the TPD's primary liveness signal for the registering visor.

The manager SHALL mirror register / deregister events to two parallel channels:

- **CXO publisher feed.** Register and deregister events are published as transport-entry leaves on the visor's stats publisher feed (the same feed used for telemetry — see *CXO Publisher* below). The TPD subscribes to this feed via the visor's PK and ingests the leaves into the transport registry.
- **HTTP POST `/v3/transports/`.** The authenticated v3 endpoint accepts the same bare-entry batch and records a heartbeat for the registering visor on each call.

Both channels SHOULD be operated concurrently as a dual-write contract; the TPD tolerates the same event arriving via either or both. Bandwidth and latency SHALL NOT appear in the registration payload on either channel — they reach the TPD via the same CXO feed under different leaves (see §Visor-Local Telemetry Store).

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

A raw-event log of per-transport bandwidth samples MAY be retained for offline diagnostic use. Its format is local to the visor and not part of any wire contract; the structured rollup store described below is the source of truth for queries and remote aggregation. Retention of the raw log is bounded by `rotation_interval`.

## Visor-Local Telemetry Store

The visor SHALL maintain a local telemetry store recording per-transport bandwidth/latency rollups, three-tier uptime intervals, and per-service uptime intervals. This store is the source of truth for the bandwidth, latency, and uptime data that the Transport Discovery, Uptime Tracker, and Service Discovery aggregate; those services act as subscribers rather than primary recorders.

### Data Model

The store SHALL retain, at minimum:

| Category | Keyed by | Holds |
|---|---|---|
| Transport rollups | transport ID | identifying fields (edges, type, label, first/last seen), a live `current` snapshot (cumulative sent/recv bytes, latency min/max/avg), and an append-only `daily` series of per-UTC-day deltas |
| Tier uptime | tier name (`process`, `dmsg`, `skynet`) per UTC date | a 288-slot bitmap, one bit per 5-minute slot |
| Service uptime | service slug (e.g. `vpn-server`, `skysocks`, `skychat`, `visor`) per UTC date | a 288-slot bitmap, one bit per 5-minute slot |

The 288-slot/5-minute uptime bitmap is the wire format shared with the Transport Discovery's uptime tracking: each bit represents one 5-minute slot of a UTC day; a set bit indicates the tier or service was online during at least one sample within the slot. The 36-byte raw form is normative; the 288-character ASCII rendering (`.` = set, ` ` = unset) is for display only.

### Sampling

A sampler SHALL run on a fixed interval (default `1m`, configurable via `StatsSampleInterval`). On each tick the visor SHALL:

- For every live transport, sample cumulative bandwidth and latency statistics, update the current UTC day's rollup delta against an in-memory day-start baseline, and overwrite the `current` snapshot.
- For every tier currently online and every registered service currently online, set the bit corresponding to the present 5-minute slot in that tier or service's bitmap for the current UTC date.

At each UTC-midnight transition the transport's daily entry SHALL be sealed (its baseline reset to the now-current cumulative counters) and a fresh bitmap SHALL be opened for every tier and service that remains online into the new day.

### Three-Tier Uptime

| Tier | Online when |
|---|---|
| `process` | Visor is running |
| `dmsg` | The visor's DMSG client is connected and not in a reconnect cycle |
| `skynet` | The visor has ≥2 live transports |

Tier state SHALL be evaluated on each sampler tick. A consumer requiring a strict "really online" view MAY compute `process AND dmsg` (or `process AND dmsg AND skynet`) by AND-ing the corresponding bitmaps for the same date.

### Per-Service Uptime

For each app registered with the Service Discovery, the sampler SHALL set the slot bit on each tick for any service that is currently running. The slug used as the bitmap key SHALL match the service name registered with the Service Discovery (e.g. `vpn-server`, `skysocks`, `skychat`). The visor process itself SHALL be tracked under the slug `visor`.

### Retention

A retention sweep SHALL run at each UTC-midnight transition. After the sweep:

- A transport's `daily` series SHALL contain at most the last `StatsRetentionDays` entries (default `30`).
- A transport record with an empty `daily` series and no recent `current` snapshot MAY be deleted.
- Tier and service bitmaps whose date is older than `StatsRetentionDays` SHALL be deleted.

### Query Surface

The store SHALL be exposed read-only over two channels:

- **HTTP-over-DMSG** on the visor's log server at `dmsg://<pk>:80`, behind the survey-whitelist authorization. The whitelist on these endpoints bounds arbitrary historical ranges (DoS protection); it is not a confidentiality measure, as the same data flows openly on the CXO feed.
  - `GET /stats/transports` — live snapshot of all current transports
  - `GET /stats/transports/history?since=<RFC3339>&until=<RFC3339>&id=<uuid>` — daily rollups within range; `id` filters to a single transport
  - `GET /stats/uptime?since=<RFC3339>` — tier bitmaps rendered as 288-character ASCII per day
  - `GET /stats/services?since=<RFC3339>` — per-service bitmaps in the same format
- **CXO publisher** (see §CXO Publisher) — the visor publishes a rolling window of the same data on its feed; subscribers (notably the Transport Discovery) receive push updates without polling.

### CXO Publisher

The visor SHALL publish telemetry on a CXO feed signed by its own keypair; the feed PK is therefore identical to the visor's PK. No separate publisher identity is generated. Aggregators that already know a visor's PK from the transport registry can subscribe directly. The feed is open to any subscriber that knows the visor's PK; its sensitivity matches that of TPD's public `/metric` aggregates.

The feed carries two classes of leaf:

- **Transport-entry leaves** that mirror register / deregister events from the transport manager (see §Re-Registration). These are how the TPD ingests a visor's live transport set without the HTTP POST round-trip.
- **Telemetry leaves** that carry sampled rollups and uptime bitmaps from the local stats store. On each publish tick (aligned with `StatsSampleInterval`) the feed SHALL carry, for the current sample:

| Key | Value |
|---|---|
| `transports/<id>/current` | Live snapshot (JSON) |
| `transports/<id>/<date>` | Daily rollup (JSON) |
| `tiers/<tier>/<date>` | 36-byte bitmap (288 bits) |
| `services/<svc>/<date>` | 36-byte bitmap (288 bits) |

Bitmaps travel as raw bytes on the wire; subscribers MAY convert to the ASCII rendering at egress. Keys older than `CXOPublishWindow` (default `7d`) SHALL be deleted at each tick. The publisher's window is rolling and is decoupled from the local store's `StatsRetentionDays`.

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
