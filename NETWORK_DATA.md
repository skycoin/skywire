# Network Data Reference

This document covers CLI commands for querying network-wide data from
the Skywire deployment services and the DHT.

All commands try the visor RPC first using the visor's deployment
configuration. Use `--direct` to skip the visor RPC.

## Deployment Service Endpoints

| Service | HTTP URL | DMSG Address |
|---|---|---|
| DMSG Discovery | http://dmsgd.skywire.skycoin.com | `dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80` |
| Transport Discovery | http://tpd.skywire.skycoin.com | `dmsg://02b307aee5c8ce1666c63891f8af25ad2f0a47a243914c963942b3ba35b9d095ae:80` |
| Address Resolver | http://ar.skywire.skycoin.com | `dmsg://03234b2ee4128d1f78c180d06911102906c80795dfe41bd6253f2619c8b6252a02:80` |
| Route Finder | http://rf.skywire.skycoin.com | `dmsg://039d89c5eedfda4a28b0c58b0b643eff949f08e4f68c8357278081d26f5a592d74:80` |
| Service Discovery | http://sd.skycoin.com | `dmsg://0204890f9def4f9a5448c2e824c6a4afc85fd1f877322320898fafdf407cc6fef7:80` |
| Uptime Tracker | http://ut.skywire.skycoin.com | `dmsg://022c424caa6239ba7d1d9d8f7dab56cd5ec6ae2ea9ad97bb94ad4b48f62a540d3f:80` |

---

## Aggregated Network View

### `skywire cli sd`

Combined view from service discovery + transport discovery. Shows
every visor with its services, country, version, and transport
breakdown by type.

```
pk                                                                         country   version         services      stcpr   sudph   dmsg   stcp   total
03d1d78e7323e1dc63a6cbbf79e52974791e3cd7b5aaab77f045d72a21b066ee8c:38567   US        v1.3.46-0       visor         329     22      0      0      351
02e26350c58c54c79e472608b6f6600ca1d139038b822c296fb2f144670052e7ce:30001   SE        v1.3.45         proxy,visor   332     0       0      0      332
```

**Data sources:**
- `/api/services` — [http://sd.skycoin.com/api/services](http://sd.skycoin.com/api/services) — `dmsg://0204890f9def4f9a5448c2e824c6a4afc85fd1f877322320898fafdf407cc6fef7:80/api/services`
- `/all-transports` — [http://tpd.skywire.skycoin.com/all-transports](http://tpd.skywire.skycoin.com/all-transports) — `dmsg://02b307aee5c8ce1666c63891f8af25ad2f0a47a243914c963942b3ba35b9d095ae:80/all-transports`

**DHT equivalent:** `skywire cli visor dht get <pk> tp`

---

## Service Health

### `skywire cli svc health`

Health check of all deployment services. Shows status, latency,
connection protocol (http or dmsg), version, and public key.

```
SERVICE               STATUS  LATENCY  PROTOCOL  VERSION                 PK
Config Service        OK      316ms    http       v1.3.46-0               -
Transport Discovery   OK      562ms    dmsg       v1.3.46-0               02b307...
DMSG Discovery        OK      567ms    dmsg       v1.3.46-0               022e60...
Address Resolver      DOWN    -        dmsg       -                       03234b...
DMSG Server           OK      548ms    dmsg       -                       0281a1...
Route Setup Node      OK      557ms    dmsg       v1.3.46-0               032457...
```

**Data source:** `/health` endpoint on each service:

| Service | HTTP | DMSG |
|---|---|---|
| DMSG Discovery | [http://dmsgd.skywire.skycoin.com/health](http://dmsgd.skywire.skycoin.com/health) | `dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80/health` |
| Transport Discovery | [http://tpd.skywire.skycoin.com/health](http://tpd.skywire.skycoin.com/health) | `dmsg://02b307aee5c8ce1666c63891f8af25ad2f0a47a243914c963942b3ba35b9d095ae:80/health` |
| Address Resolver | [http://ar.skywire.skycoin.com/health](http://ar.skywire.skycoin.com/health) | `dmsg://03234b2ee4128d1f78c180d06911102906c80795dfe41bd6253f2619c8b6252a02:80/health` |
| Route Finder | [http://rf.skywire.skycoin.com/health](http://rf.skywire.skycoin.com/health) | `dmsg://039d89c5eedfda4a28b0c58b0b643eff949f08e4f68c8357278081d26f5a592d74:80/health` |
| Service Discovery | [http://sd.skycoin.com/health](http://sd.skycoin.com/health) | `dmsg://0204890f9def4f9a5448c2e824c6a4afc85fd1f877322320898fafdf407cc6fef7:80/health` |
| Uptime Tracker | [http://ut.skywire.skycoin.com/health](http://ut.skywire.skycoin.com/health) | `dmsg://022c424caa6239ba7d1d9d8f7dab56cd5ec6ae2ea9ad97bb94ad4b48f62a540d3f:80/health` |

---

## Transport Discovery Data

### `skywire cli tp net-stats`

Aggregate network statistics.

```
.:: Network Transport Statistics ::.
Total Transports: 4354
Unique Visors: 920
By Type:
  STCPR: 1232
  SUDPH: 3115
  DMSG: 7
```

**Data source:** `/all-transports/stats`
- [http://tpd.skywire.skycoin.com/all-transports/stats](http://tpd.skywire.skycoin.com/all-transports/stats)
- `dmsg://02b307aee5c8ce1666c63891f8af25ad2f0a47a243914c963942b3ba35b9d095ae:80/all-transports/stats`

### `skywire cli tp tpd-stats`

Per-visor transport counts by type, sorted by total.

```
PK                                                                  TOTAL  STCPR  SUDPH  DMSG
03d1d78e7323e1dc63a6cbbf79e52974791e3cd7b5aaab77f045d72a21b066ee8c  323    301    22     0
02e26350c58c54c79e472608b6f6600ca1d139038b822c296fb2f144670052e7ce  321    321    0      0
027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed  300    285    15     0
```

`--top 5`:
```
skywire cli tp tpd-stats --top 5
```

`--type stcpr --min 100`:
```
skywire cli tp tpd-stats --type stcpr --min 100
```

**Data source:** `/all-transports/per-key-stats`
- [http://tpd.skywire.skycoin.com/all-transports/per-key-stats](http://tpd.skywire.skycoin.com/all-transports/per-key-stats)
- `dmsg://02b307aee5c8ce1666c63891f8af25ad2f0a47a243914c963942b3ba35b9d095ae:80/all-transports/per-key-stats`

### `skywire cli tp metrics`

Per-visor bandwidth and latency. Shows verified bandwidth (both edges agree).

Default (1 day, all visors):
```
skywire cli tp metrics --top 5

public_key                                                           transports   sent       recv       bandwidth
027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed   322          26.14MB    26.06MB    52.21MB
03d1d78e7323e1dc63a6cbbf79e52974791e3cd7b5aaab77f045d72a21b066ee8c   310          21.95MB    21.18MB    43.13MB

5 visors, 1437 transports, 131.66MB network bandwidth (1 days)
```

7-day window:
```
skywire cli tp metrics --days 7 --top 3

public_key                                                           transports   sent      recv      bandwidth
027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed   278          53.64MB   54.13MB   107.77MB
03dc3488e49ded9250f3cca2827ab16f6331b7ccefdad614353c785a5fc76c1b13   226          36.40MB   36.44MB   72.83MB
03d1d78e7323e1dc63a6cbbf79e52974791e3cd7b5aaab77f045d72a21b066ee8c   295          24.26MB   22.38MB   46.64MB

3 visors, 1798 transports, 273.72MB network bandwidth (7 days)
```

Per-transport for a specific visor:
```
skywire cli tp metrics --by-transport --pk 03d1d78e... --top 3

transport_id                           type    sent       recv       bandwidth   latency
5c18d06a-1959-0a12-a6b7-4f6d5ec39ed1   stcpr   275.01KB   275.01KB   550.03KB    -
6c2c997e-968c-0c8a-b7bc-b3827d382a2e   stcpr   260.35KB   260.35KB   520.71KB    -
```

**Data source:** `/metrics?days=<n>&bandwidth=true&latency=true&edges=true`
- [http://tpd.skywire.skycoin.com/metrics?days=1&bandwidth=true&latency=true&edges=true](http://tpd.skywire.skycoin.com/metrics?days=1&bandwidth=true&latency=true&edges=true)
- `dmsg://02b307aee5c8ce1666c63891f8af25ad2f0a47a243914c963942b3ba35b9d095ae:80/metrics?days=1&bandwidth=true&latency=true&edges=true`
- 7-day: [http://tpd.skywire.skycoin.com/metrics?days=7&bandwidth=true&latency=true&edges=true](http://tpd.skywire.skycoin.com/metrics?days=7&bandwidth=true&latency=true&edges=true)
- Per-visor: `?pk=03d1d78e...`

### `skywire cli svc tpd bandwidth`

Network-wide bandwidth with daily breakdown and per-type latency.

```
skywire cli svc tpd bandwidth

Cumulative: 797.1 MB
  stcpr: 786.3 MB
  sudph: 10.8 MB
Daily:
  Day 0: 327.3 MB
    stcpr: 324.0 MB, latency 167ms
    sudph: 3.3 MB, latency 105ms
```

Per-visor:
```
skywire cli svc tpd bandwidth --pk 03d1d78e7323e1dc63a6cbbf79e52974791e3cd7b5aaab77f045d72a21b066ee8c
```

**Data source:** `/bandwidth`
- [http://tpd.skywire.skycoin.com/bandwidth](http://tpd.skywire.skycoin.com/bandwidth)
- `dmsg://02b307aee5c8ce1666c63891f8af25ad2f0a47a243914c963942b3ba35b9d095ae:80/bandwidth`
- Per-visor: `/bandwidth/visor/<pk>` — [http://tpd.skywire.skycoin.com/bandwidth/visor/03d1d78e...](http://tpd.skywire.skycoin.com/bandwidth/visor/03d1d78e7323e1dc63a6cbbf79e52974791e3cd7b5aaab77f045d72a21b066ee8c)

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

**Data source:** `/versions`
- [http://tpd.skywire.skycoin.com/versions](http://tpd.skywire.skycoin.com/versions)
- `dmsg://02b307aee5c8ce1666c63891f8af25ad2f0a47a243914c963942b3ba35b9d095ae:80/versions`

### `skywire cli tp tree`

Transport network tree. Top visors by transport count with type breakdown.

```
skywire cli tp tree --stats

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

**Data source:** `/all-transports`
- [http://tpd.skywire.skycoin.com/all-transports](http://tpd.skywire.skycoin.com/all-transports)
- `dmsg://02b307aee5c8ce1666c63891f8af25ad2f0a47a243914c963942b3ba35b9d095ae:80/all-transports`

### `skywire cli tp uptime`

Transport-level uptime with daily percentages.

**Data source:** `/uptimes/transports?v=v2`
- [http://tpd.skywire.skycoin.com/uptimes/transports?v=v2](http://tpd.skywire.skycoin.com/uptimes/transports?v=v2)
- `dmsg://02b307aee5c8ce1666c63891f8af25ad2f0a47a243914c963942b3ba35b9d095ae:80/uptimes/transports?v=v2`
- By transport IDs: `/metrics/uptime/<id1>,<id2>`
- By visor PK: `/metrics/uptime/visor/<pk>` — [http://tpd.skywire.skycoin.com/metrics/uptime/visor/03d1d78e...](http://tpd.skywire.skycoin.com/metrics/uptime/visor/03d1d78e7323e1dc63a6cbbf79e52974791e3cd7b5aaab77f045d72a21b066ee8c)

---

## DMSG Discovery Data

### `skywire cli mdisc servers`

List all DMSG servers with address and available session count.

```
version     registered              public-key                           address                   avail-sess
0.0.1       1776634424622687823     0281a102c82820e8...                  139.162.160.227:30086     1738
0.0.1       1776634424773073516     0371ab4bcff7b121...                  139.162.160.227:30087     1713
```

**Data source:** `/dmsg-discovery/available_servers`
- [http://dmsgd.skywire.skycoin.com/dmsg-discovery/available_servers](http://dmsgd.skywire.skycoin.com/dmsg-discovery/available_servers)
- `dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80/dmsg-discovery/available_servers`

**DHT equivalent:** `skywire cli visor dht get <server-pk> dmsg`

### `skywire cli mdisc entry <pk>`

Fetch a visor's DMSG discovery entry (delegated servers, client type).

**Data source:** `/dmsg-discovery/entry/<pk>`
- [http://dmsgd.skywire.skycoin.com/dmsg-discovery/entry/03d1d78e...](http://dmsgd.skywire.skycoin.com/dmsg-discovery/entry/03d1d78e7323e1dc63a6cbbf79e52974791e3cd7b5aaab77f045d72a21b066ee8c)
- `dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80/dmsg-discovery/entry/<pk>`

**DHT equivalent:** `skywire cli visor dht get <pk> dmsg`

### `skywire cli svc dmsgd all-servers`

Same data as `mdisc servers`.

### `skywire cli svc dmsgd clients --pk <server-pk>`

List clients connected to a specific DMSG server.

**Data source:** `/dmsg-discovery/visor_entries/<server-pk>`
- [http://dmsgd.skywire.skycoin.com/dmsg-discovery/visor_entries/0281a102...](http://dmsgd.skywire.skycoin.com/dmsg-discovery/visor_entries/0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb)
- `dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80/dmsg-discovery/visor_entries/<server-pk>`

### `skywire cli svc dmsgd server-clients`

All clients grouped by server.

**Data source:** `/dmsg-discovery/all_servers_clients`
- [http://dmsgd.skywire.skycoin.com/dmsg-discovery/all_servers_clients](http://dmsgd.skywire.skycoin.com/dmsg-discovery/all_servers_clients)
- `dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80/dmsg-discovery/all_servers_clients`

---

## Service Discovery Data

### `skywire cli pv`

List public visors from service discovery.

**Data source:** `/api/services?type=visor`
- [http://sd.skycoin.com/api/services?type=visor](http://sd.skycoin.com/api/services?type=visor)
- `dmsg://0204890f9def4f9a5448c2e824c6a4afc85fd1f877322320898fafdf407cc6fef7:80/api/services?type=visor`
- With country: `/api/services?type=visor&country=US` — [http://sd.skycoin.com/api/services?type=visor&country=US](http://sd.skycoin.com/api/services?type=visor&country=US)

---

## Uptime Data

### `skywire cli ut`

Standalone uptime tracker data.

**Data source:** `/uptimes?v=v2`
- [http://ut.skywire.skycoin.com/uptimes?v=v2](http://ut.skywire.skycoin.com/uptimes?v=v2)
- `dmsg://022c424caa6239ba7d1d9d8f7dab56cd5ec6ae2ea9ad97bb94ad4b48f62a540d3f:80/uptimes?v=v2`

### `skywire cli ut tpd`

TPD-integrated uptime. Considers a visor online when it has 2+ transports.

**Data source:** `/uptimes?v=v2`
- [http://tpd.skywire.skycoin.com/uptimes?v=v2](http://tpd.skywire.skycoin.com/uptimes?v=v2)
- `dmsg://02b307aee5c8ce1666c63891f8af25ad2f0a47a243914c963942b3ba35b9d095ae:80/uptimes?v=v2`

### `skywire cli ut mdisc`

DMSG discovery integrated uptime.

**Data source:** `/uptimes?v=v2`
- [http://dmsgd.skywire.skycoin.com/uptimes?v=v2](http://dmsgd.skywire.skycoin.com/uptimes?v=v2)
- `dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80/uptimes?v=v2`

### `skywire cli ut sd`

Service discovery integrated uptime.

**Data source:** `/uptimes?v=v2`
- [http://sd.skycoin.com/uptimes?v=v2](http://sd.skycoin.com/uptimes?v=v2)
- `dmsg://0204890f9def4f9a5448c2e824c6a4afc85fd1f877322320898fafdf407cc6fef7:80/uptimes?v=v2`

---

## Route Setup Node Statistics

### `skywire cli route rsn-stats`

Request statistics from the embedded route setup node.

```
Started:          2026-04-19T13:59:33-05:00
Uptime:           2h36m
Total requests:   71182
Successful:       24847
Failed:           46335
Success rate:     34.9%

Latency (ms) over last 1024 successful setups:
  min=77  mean=1057  p50=871  p95=1563  p99=6157

Failures by reason:
  source_unreachable     18634
  circuit_open           16559
```

**Data source:** Visor RPC → embedded RSN `Collector.Snapshot()`

### `skywire cli route rsn-remote-stats`

Query a standalone RSN's `/stats` endpoint over DMSG. Uses the
visor's DMSG connection (RPC → DMSG HTTP). Defaults to the first
RSN PK from the visor's config.

```bash
skywire cli route rsn-remote-stats
skywire cli route rsn-remote-stats --pk 0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557
```

**Data source:** `/stats`
- `dmsg://0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557:80/stats`

The standalone RSN has no HTTP interface — DMSG is the only path.

---

## Transport Setup Node Data

### `skywire cli tp --remote <pk>`

Query a remote visor's transports via the Transport Setup Node (TPS).
Returns transports with labels `skycoin` and `automatic` only —
user-labeled transports are not exposed by the TPS.

```
skywire cli tp --remote 03d1d78e7323e1dc63a6cbbf79e52974791e3cd7b5aaab77f045d72a21b066ee8c

[1/1] 03d1d78e... (277 transports)
  type    id                                     local              remote
  stcpr   a2bfdcc8-09ba-093a-bacd-aef5b8005c62   03d1d78e...        03b8a672...
```

**Data source:** TPS DMSG RPC on port 47 (`DmsgTransportSetupPort`)

**TPS constraints:**
- **GetTransports:** returns `skycoin` + `automatic` labels only (not `user`)
- **AddTransport:** creates with label `skycoin`
- **RemoveTransport:** only `skycoin` + `automatic` labels, and only if no active route uses the transport

### `skywire cli tps`

Manage transports via the Transport Setup Node.

```bash
skywire cli tps list --pk <visor-pk>   # list remote visor's transports
skywire cli tps add --pk <visor-pk> --remote <remote-pk> --type stcpr
skywire cli tps rm --pk <visor-pk> --id <transport-id>
```

---

## DHT Data

### `skywire cli visor dht status`

DHT node status. Uses visor RPC.

### `skywire cli visor dht get <pk> [salt]`

Retrieve a value from the DHT via visor RPC. Salt selects the data type:
- `dmsg` — DMSG discovery entry
- `tp` — transport list
- `svc` — service record
- (empty) — default namespace

**Data source:** Kademlia DHT (local store + iterative network lookup via visor RPC)

---

## Address Resolver

### `skywire cli svc ar check <pk>`

Check if a public key is registered in the address resolver without
revealing its IP address. Returns transport types the visor is
registered for (stcpr, sudph) or "not found".

```
skywire cli svc ar check 03d1d78e7323e1dc63a6cbbf79e52974791e3cd7b5aaab77f045d72a21b066ee8c
03d1d78e...: registered for [stcpr sudph]
```

**Data source:** `/resolve/<type>/<pk>`
- `dmsg://03234b2ee4128d1f78c180d06911102906c80795dfe41bd6253f2619c8b6252a02:80/resolve/stcpr/<pk>`

Note: the full resolve response (with IP address) is not exposed via
CLI for privacy reasons. Only the existence of the entry is returned.

---

