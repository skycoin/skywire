# Network Data Reference

This document covers CLI commands for querying network-wide data from
the Skywire deployment services (or from the DHT). These commands
consume data from DMSG discovery, transport discovery, service
discovery, address resolver, and uptime tracker endpoints.

All commands try the visor RPC first (uses the visor's configured
deployment URLs), then fall back to direct HTTP. Use `--direct` to
skip the visor RPC.

## Aggregated Network View

### `skywire cli sd`

Combined view from service discovery + transport discovery. Shows
every visor with its services, country, version, and transport
breakdown by type.

```
pk                                                                         country   version         services      stcpr   sudph   dmsg   stcp   total
03d1d78e7323e1dc63a6cbbf79e52974791e3cd7b5aaab77f045d72a21b066ee8c:38567   US        v1.3.46-0       visor         329     22      0      0      351
02e26350c58c54c79e472608b6f6600ca1d139038b822c296fb2f144670052e7ce:30001   SE        v1.3.45         proxy,visor   332     0       0      0      332
027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed:7778    US        v1.3.46-0       visor         307     15      0      0      322
```

**Data sources:** Service Discovery (`/api/services`), Transport Discovery (`/all-transports`)

**DHT equivalent:** `dht get <pk> svc` (per-visor service record), `dht get <pk> tp` (per-visor transport list)

## Service Health

### `skywire cli svc health`

Health check of all deployment services. Shows status, latency, transport
mode (http/dmsg), version, and public key.

```
SERVICE               STATUS  LATENCY  TRANSPORT  VERSION                 PK
Config Service        OK      387ms    http       v1.3.46-0               -
Transport Discovery   OK      674ms    dmsg       v1.3.46-0               02b307...
DMSG Discovery        OK      674ms    dmsg       v1.3.46-0               022e60...
DMSG Server           OK      554ms    dmsg       v1.3.46-0               0281a1...
```

**Data source:** `/health` endpoint on each service

## Transport Discovery Data

### `skywire cli tp net-stats`

Aggregate network statistics: total transports, unique visors, breakdown by type.

```
.:: Network Transport Statistics ::.
Total Transports: 4354
Unique Visors: 920
By Type:
  STCPR: 1232
  SUDPH: 3115
  DMSG: 7
```

**Data source:** Transport Discovery

### `skywire cli tp tpd-stats`

Per-visor transport counts, sorted by total. Shows STCPR, SUDPH, DMSG breakdown.

```
PK                                                                  TOTAL  STCPR  SUDPH  DMSG
03d1d78e...  304    282    22     0
03dc3488...  289    289    0      0
```

Flags: `--top <n>`, `--type <type>`, `--min <n>`

**Data source:** Transport Discovery (`/all-transports/per-key-stats`)

### `skywire cli tp metrics`

Per-visor bandwidth and latency. Verified bandwidth (both edges agree).

```
public_key                                                           transports   sent       recv       bandwidth
027087fe40d97f7f...   322          26.14MB    26.06MB    52.21MB
03d1d78e7323e1dc...   310          21.95MB    21.18MB    43.13MB

5 visors, 1437 transports, 131.66MB network bandwidth (1 days)
```

Flags:
- `--days <n>` — time window (1=today, 7=week, 0=all, max 35)
- `--top <n>` — show only top N visors
- `--pk <pk>` — filter to specific visor
- `--by-transport` — show per-transport instead of per-visor
- `--tree` — tree view with visors and their transports

**Data source:** Transport Discovery (`/metrics`)

### `skywire cli svc tpd bandwidth`

Network-wide bandwidth: cumulative total and daily breakdown with
per-type latency averages.

```
Cumulative: 797.1 MB
  stcpr: 786.3 MB
  sudph: 10.8 MB
Daily:
  Day 0: 327.3 MB
    stcpr: 324.0 MB, latency 167ms
    sudph: 3.3 MB, latency 105ms
```

Flags: `--pk <pk>` for per-visor bandwidth

**Data source:** Transport Discovery (`/bandwidth`)

### `skywire cli svc tpd versions`

Version distribution across the network.

```json
{
  "v1.3.45": 728,
  "unknown": 311,
  "v1.3.43": 112,
  "v1.3.37": 66,
  "v1.3.46-0": 7
}
```

**Data source:** Transport Discovery (`/versions`)

### `skywire cli tp tree`

Tree visualization of the transport network. Top visors by transport
count with type breakdown.

```
Unique keys in Transport Discovery: 920
Count of transports: 4354
Types of transports:
  stcpr: 1232
  sudph: 3115
  dmsg: 7
Top visors by transport count:
  1: 03d1d78e... (count=351, version=v1.3.46-0)
  2: 02e26350... (count=332, version=v1.3.45)
```

Flags: `-k <pk>` (root node), `-d <pk>` (map route to dest), `--stats`

**Data source:** Transport Discovery (`/all-transports`)

### `skywire cli tp uptime`

Transport-level uptime data with daily percentage and timeline bitmaps.

Flags: `--ids <id,...>`, `--visors <pk,...>`, `--metrics`

**Data source:** Transport Discovery (`/uptimes/transports`)

### `skywire cli tp viz`

Interactive web-based network graph visualization. Force-directed graph
with visors as nodes, transports as edges, colored by type.

**Data source:** Transport Discovery (`/all-transports`)

## DMSG Discovery Data

### `skywire cli mdisc servers`

List all DMSG servers with address and available session count.

```
version     registered              public-key                           address                   avail-sess
0.0.1       1776634424622687823     0281a102c82820e8...                  139.162.160.227:30086     1738
0.0.1       1776634424773073516     0371ab4bcff7b121...                  139.162.160.227:30087     1713
```

**Data source:** DMSG Discovery (`/dmsg-discovery/available_servers`)

**DHT equivalent:** `dht get <server-pk> dmsg`

### `skywire cli mdisc entry <pk>`

Fetch a specific visor's DMSG discovery entry (delegated servers, client type).

**Data source:** DMSG Discovery (`/dmsg-discovery/entry/<pk>`)

**DHT equivalent:** `dht get <pk> dmsg`

### `skywire cli svc dmsgd all-servers`

List all DMSG servers (same as `mdisc servers` but via svc subcommand).

### `skywire cli svc dmsgd clients --pk <server-pk>`

List clients connected to a specific DMSG server.

### `skywire cli svc dmsgd server-clients`

All clients grouped by server.

## Service Discovery Data

### `skywire cli pv`

List public visors from service discovery. Returns PKs of visors
registered as type "visor".

Flags: `-t` (show transport counts), `--country <code>`

**Data source:** Service Discovery (`/api/services?type=visor`)

## Uptime Data

### `skywire cli ut`

Query the standalone uptime tracker. Shows visors meeting minimum
uptime threshold.

Flags: `-n <min-pct>` (minimum uptime %), `-k <pk>` (specific visor)

### `skywire cli ut tpd`

Query TPD-integrated uptime (replaces standalone UT). Shows per-visor
daily uptime percentages with graph visualization.

### `skywire cli ut mdisc`

Query DMSG discovery integrated uptime.

### `skywire cli ut sd`

Query service discovery integrated uptime.

**Note:** The uptime tracking is integrated per-service: TPD considers
a visor online when it has 2+ transports. DMSG discovery considers it
online when it has a valid entry. Service discovery considers it online
when it registers services.

## Route Setup Node Statistics

### `skywire cli route rsn-stats`

Embedded route setup node request statistics: success rate, latency
percentiles, failure breakdown, top destinations.

```
Started:          2026-04-19T12:00:00-05:00
Uptime:           5h30m
Total requests:   50000
Success rate:     71.1%
Latency p50:      980ms  p95: 1508ms

Failures by reason:
  source_unreachable     10117
  circuit_open           664
  id_reservation         586
```

Flags: `--reset` (clear counters), `--json`

**Data source:** Visor RPC (embedded RSN `Collector.Snapshot()`)

## DHT Data

### `skywire cli visor dht status`

DHT node status: routing peers, stored items by trust tier.

### `skywire cli visor dht get <pk> [salt]`

Retrieve a value from the DHT. Salt selects the data type:
- `dmsg` — DMSG discovery entry
- `tp` — transport list
- `svc` — service record
- (empty) — default namespace

**Data source:** Kademlia DHT (local store + iterative network lookup)

## Gaps and Missing Data

The following network data is not currently queryable via CLI:

| Data | Available from | CLI gap |
|---|---|---|
| Per-transport latency history | TPD `/metrics` | `tp metrics --by-transport` shows it but latency field often zero |
| Address resolver entries | AR | No CLI to query AR entries for a PK |
| Per-visor bandwidth over time | TPD `/bandwidth` | `svc tpd bandwidth --pk` exists but output is raw JSON |
| DMSG server load history | DMSG servers (pprof) | No CLI; requires SSH tunnel to pprof port |
| RSN stats for standalone RSN | RSN DMSG HTTP `/stats` | No CLI; only embedded RSN has `rsn-stats` |
| DHT network size | DHT routing table | `dht status` shows local peers but not network-wide estimate |
