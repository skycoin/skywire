# Transport Discovery

| Status   | Implemented                                                        |
|----------|--------------------------------------------------------------------|
| Revision | 2026-10-27, skywire c18b044dc |
| Code     | `pkg/deployment/tpd`, `pkg/services/tpd`, `pkg/transport`, `pkg/visor` |

## Abstract

Transport discovery (TPD) is the deployment service that records which
transports exist between visors. It ingests each visor's own transport list,
expires entries that are not refreshed, answers queries over HTTP, and
republishes aggregates as CXO feeds. A visor's transports are also available
from the visor itself.

## Overview

Visors report to TPD, and route finders, address resolvers, the reward system
and visors read from it. The authoritative source for one visor's transports is
that visor, so a reader that needs them can bypass TPD. See
[Transport data from visors](#transport-data-from-visors).

## Addressing

TPD is served over dmsg HTTP at `dmsg://<tpd-pk>:80`. The key is
`transport_discovery_dmsg` in `deployment.Services`. Its CXO feeds are served on
the dmsg ports in [Interfaces](#cxo-feeds) under the same key.

## Data Formats

`transport.Entry` (`pkg/transport/entry.go`):

| JSON name | Type | Meaning |
|---|---|---|
| `t_id` | UUID | Transport ID. `transport.MakeTransportID` derives it from the two edges and the type. |
| `edges` | array of two public keys | Edges in ascending order. |
| `type` | string | `tptypes.Type`: `stcpr`, `sudph`, `stcp`, `dmsg`, `squicr`, `swsr`, `swtr`, `webrtc`. |
| `label` | string | `transport.Label`: `user`, `automatic`, `skycoin`, `setup`, `sysfeed`. |
| `latency_ms` | number | Round trip time. Omitted when zero. |
| `bandwidth` | integer | Total bytes. Omitted when zero. |
| `throughput_bps` | number | Observed peak goodput. Omitted when zero. |

`transport.SignedEntry` wraps an entry for the legacy registration route:
`entry`, `signatures` (two signatures), `registered`, `latency`, `bandwidth`,
`version`. TPD stores only the ID, edges, type, label and update time.

`transport.CompactEntry` is the form used on visor feeds and in queries:

| JSON name | Type | Meaning |
|---|---|---|
| `r` | public key | The edge that is not the reporting visor. |
| `t` | string | Transport type. |
| `l` | string | Label. Omitted for `automatic`. |

A reader rebuilds the full entry with `transport.EntryFromCompact(reporter, ce)`.

`store.TransportSummary`: `total_transports`, `by_type` (map), `unique_visors`,
and `partial` and `missing_batches` when a read failed. `store.VisorSummary`:
`pk`, `on`, `version`, `daily`, `timeline`. `store.TransportUptimeSummary`:
`id`, `on`, `type`, `edge_a`, `edge_b`, `daily`, `timeline`. Error bodies are
`{"error": "<text>"}` (`api.Error`).

## Interfaces

### HTTP API

Every route in `pkg/deployment/tpd/api/api.go`. Routes in the first two groups
are limited to 30 requests per minute with a burst of 10, per caller. The caller
is the dmsg peer key, or `RemoteAddr` when it is not a key. A rejected request
returns 429. Request bodies are limited to `maxBodyBytes` (8 MiB) and a larger
body returns 413. Bodies of 1024 bytes or more (`httputil.CompressMinBytes`) are
gzip compressed when the client accepts it.

Status codes common to the handlers that call `writeError`: 400 for an invalid
public key or transport ID or a JSON syntax error, 404 for
`store.ErrTransportNotFound`, 409 for `store.ErrAlreadyRegistered`, 408 for a
deadline, 413 for a body over the limit, and 500 for anything else.

#### Authenticated routes

Authentication is [02](02-HTTP_Authorization_Middleware.md). A dmsg caller is
identified by its dmsg key.

| Method and path | Request | Response |
|---|---|---|
| `GET /transports/id:{uuid}` | | 200 `Entry`. 400, 404. |
| `GET /transports/edge:{pk}` | | 200 array of `Entry`, gzip when accepted. 400. 404 when the visor has no transports. |
| `POST /transports/` | array of `SignedEntry` | 201 with the request array. 400, 500. |
| `POST /v3/transports/` | array of `Entry`, header `SW-Visor-Version` | 201 with the accepted entries, an empty array when none. |
| `DELETE /transports/id:{uuid}` | | 200 with the text `transport deleted`. 400 when the ID is invalid or the caller is not an edge. 404. |
| `POST /transports/delete-batch` | array of ID strings | 200 `{"deleted": n, "skipped": n}`. A body that is not a JSON array returns 500. |
| `GET /v4/update?version={v}` | | 200 `{"status":"ok"}`. Records a visor heartbeat. 500. |

Any other `kind:` in `/transports/{ref}` returns 404. In
`POST /v3/transports/` an entry is accepted only when the caller is one of its
edges, and other entries are skipped. Both registration routes record a
transport heartbeat for each entry and a visor heartbeat for the caller.
`DELETE /transports/id:{uuid}` requires the caller to be an edge of the
transport. `POST /transports/delete-batch` skips IDs that are invalid, unknown,
not owned by the caller, or fail to delete.

#### Public routes

| Method and path | Response |
|---|---|
| `POST /transports/edges` | Body is an array of public key strings. 200 array of `Entry` for the union of the keys, deduplicated by ID, empty array when none. 400 for a body that is not a JSON array. |
| `GET /v3/transports/edge:{pk}` | 200 array of `Entry`, empty array when none. 400. |
| `GET /all-transports` | 410 `{"error": ...}`. The list is served on the routing feed. |
| `GET /all-transports/stats?selfTransports=hide` | 200 `TransportSummary`. 404 when there are no transports. `hide` excludes self transports. |
| `GET /all-transports/per-key-stats?selfTransports=hide` | 200 object mapping a public key to its counts by type and `total`. 404 when empty. |
| `GET /transports/stats/{edge}` | 200 `{"total": n, "by_type": {...}}`. 400. 404 when the visor has none. |
| `DELETE /transports/deregister` | Deregistration whitelist only, see [02](02-HTTP_Authorization_Middleware.md#deregistration-whitelist). Body is an array of transport IDs. 403 for an unlisted key or a bad signature. 400 for a bad key, signature or body. 200 when finished. |
| `GET /bandwidth/transport/{id}?period=daily&limit=7` | 200 bandwidth history. 400, 404. |
| `GET /bandwidth/visor/{pk}?period=daily&limit=7` | 200 bandwidth history for the visor's transports. 400. |
| `GET /metric` | 200 network aggregate. |
| `GET /metric/visor/{pks}` | 200 aggregate for comma separated visors. 400. |
| `GET /metrics` | 200 per transport metrics. |
| `GET /metrics/{ids}` | 200 metrics for comma separated transport IDs. 400. |
| `GET /metrics/visor/{pks}` | 200 per transport metrics for comma separated visors. 400. |
| `GET /uptimes?v={v2,v3}&visors={pk;pk}` | 200 array of `VisorSummary`. Without `v` only `pk`, `on` and `version`. `v2` adds `daily`. `v3` adds `timeline`. |
| `POST /uptimes` | Body `{"pks": [...], "v": "v2"}`. Same response as `GET`. 400 for invalid JSON or an unknown `v`. |
| `GET /uptimes/transports?v=&visors=&tp=&type=&edges=true` | 200 array of `TransportUptimeSummary`. `visors` and `tp` are semicolon separated. `edges=true` includes `edge_a` and `edge_b`. |
| `POST /uptimes/transports` | Body `{"pks","ids","type","edges","v"}`. Same response. 400 for invalid JSON. |
| `GET /metrics/uptime` | 200 `{"total_transports","online","by_type":{type:{"total","online"}}}`. |
| `GET /metrics/uptime/{ids}` | 200 transport uptime for comma separated IDs. 400. |
| `GET /metrics/uptime/visor/{pks}` | 200 transport uptime for comma separated visors. 400. |
| `GET /version?on=` | 200 map from version to count. `on` is `true`, `false`, `all` or `none`. |
| `GET /versions?on=&status=true` | 200 array of `{"pk","version","on"}`. |
| `GET /versions/{pks}` | Same for comma separated keys. An unknown key appears with an empty version. 400. |

#### Other routes

| Method and path | Response |
|---|---|
| `GET /health` | 200 `httputil.HealthCheckResponse` with service name `transport-discovery`. |
| `GET /security/nonces/{pk}` | Nonce recovery, see [02](02-HTTP_Authorization_Middleware.md). |
| `GET /` | The charts page. 404 until the charts are running. |
| `GET /graph`, `/graph/engine.wasm`, `/graph/engine.js` | The transport graph page and its engine. `/graph` returns 404 until the charts run and 503 while the transport cache is cold. |
| `POST /statuses` | 410. |
| `GET /uptime/now`, `/uptime/sessions`, `/uptime/timeline`, `/uptime/dates` | The service's own uptime record. 503 until a recorder is set. |

### CXO Feeds

Ports are constants in `pkg/skyenv`. Layout and cadence are in
[31](31-CXO_Feeds.md). TPD aggregates two visor feeds and publishes six.

| Port | Identifier | Direction | Content |
|---|---|---|---|
| 50 | `DmsgCXOPort` | aggregated from each visor | Visor telemetry and, as a fallback, the `tp-list` leaf. |
| 69 | `DmsgVisorTPListCXOPort` | aggregated from each visor | The visor's own transport list at `tp-list`. |
| 51 | `DmsgTPDMetricsCXOPort` | published | `metrics/day/<YYYY-MM-DD>`. |
| 52 | `DmsgTPDUptimeCXOPort` | published | `uptimes/days/<n>`. |
| 73 | `DmsgTPDStatsCXOPort` | published | `stats/network`, `stats/versions`, `stats/daily`. |
| 75 | `DmsgTPDRoutingCXOPort` | published | `routing/<pk>` and `routing/delta/<seq>`. |
| 77 | `DmsgTPDVisorBWCXOPort` | published | `visorbw/day/<YYYY-MM-DD>`. |
| 79 | `DmsgTPDPerKeyCXOPort` | published | `perkey/stats`. |
| 55 | `DmsgTPDAllTransportsCXOPort` | none | TPD starts no publisher on this port. Subscribers still map the prefix `transports/all/`. |

## Behaviour

### Registration

A visor registers its transports by publishing its list, not by calling HTTP.
When the visor has a CXO publisher for the TPD, the transport manager
(`transport.Manager.publishTPDList`) writes the full list as the `tp-list` leaf.
It does so every `transportReRegisterInterval` (90 seconds) and, after a
deletion debounce of `deletionDebounce` (5 seconds), after a transport closes.
A visor without a CXO publisher calls `POST /v3/transports/` instead and
removes closed transports with `POST /transports/delete-batch`.

The visor announces to TPD (`treestore.Publisher.AnnounceTo`). TPD's aggregator
(`cxoaggregator`) holds the inbound connection and subscribes to the feed whose
key equals the remote key. TPD applies a `tp-list` leaf with
`API.ReconcileTransportsFromCXO`.

- Only entries of which the reporter is an edge are accepted.
- Every accepted entry is written, or its lifetime extended when unchanged and
  due, and the reporter's heartbeat is recorded.
- The reporter's stored transports that are absent from the list are
  deregistered, unless the other edge still lists them.
- When a fetch fails, the aggregator re-applies the last good list with
  `API.RefreshTransportsFromCXO`, which deletes nothing.

The aggregator also reads the legacy leaves `transports/list`,
`transports/<uuid>/entry` and `transports/<uuid>/tombstone`, the telemetry
shards `transports/telemetry/<sh>`, the legacy `transports/<uuid>/current`, and
`transports/<uuid>/<YYYY-MM-DD>/timeline`.

### Deregistration

A transport is removed in four ways.

- The registering visor omits it from its next `tp-list` leaf.
- The registering visor calls `DELETE /transports/id:{uuid}` or
  `POST /transports/delete-batch`, or an older visor publishes a
  `transports/<uuid>/tombstone` leaf.
- A key in the deregistration whitelist calls `DELETE /transports/deregister`.
- The entry expires.

### Entry timeout

Each stored entry has a lifetime of the configured `EntryTimeout`, default
`tpd.DefaultEntryTimeout` (5 minutes). Registration and refresh reset it.
Unchanged entries from the same reporter are refreshed after a third of the
timeout (`API.SetEntryTimeout`).

### Caches

| Value | Identifier |
|---|---|
| 30 seconds | `transportsCacheDelay`, refresh of the in memory transport list |
| 5 minutes | `uptimesCacheDelay`, refresh of the uptime summaries |
| 1 hour | `backupTickerDelay`, bandwidth archive and timeline archive |

## Transport data from visors

TPD is not the only source of a visor's transports. Each of the following
returns the visor's own list.

- **CXO feed.** Each visor publishes its list on `DmsgVisorTPListCXOPort` (69)
  under its own key (`buildTPListPublisher`, `pkg/visor/init_stats.go`). The root
  has one leaf, `tp-list`, which is gzip of the JSON
  `{"version": "<build version>", "c": [CompactEntry...]}`. TPD aggregates it.
  Any subscriber on the feed's allowlist can read it. The allowlist is the
  visor's peer whitelist plus the TPD key (`composeFeedAllowlist`). If the
  dedicated publisher fails to start, the visor publishes the same leaf on its
  telemetry feed at port 50.
- **Transport query.** Every visor listens on `DmsgTransportQueryPort` (68)
  (`router.ServeTransportQueryListener`). The service is a gob RPC server
  (`gobrpc`) named `TransportQueryRPCGateway` with the method `Query`. The
  request is `router.TransportQuery` and the reply is
  `router.TransportQueryResponse` (`target_pk` and `entries` as
  `CompactEntry`). The visor answers only when the query's `TargetPK` is its
  own key, `RSNPK` is one of its trusted route setup nodes, and `RSNSig`
  verifies over `SignablePayload`. The route setup node signs the query through
  the setup RPC `SignTransportQuery` ([13](13-Setup_Node.md)). The reply omits
  closed transports and transports labelled `setup`. A stream has a 30 second
  deadline. The router uses it to build two hop routes without TPD.
- **Visor RPC.** `DmsgVisorRPCPort` (65) serves the visor's net/rpc API,
  including `Transports`. Access is limited to the visor's hypervisors and
  dmsgpty whitelist. The router uses it as a second source for destinations
  that trust the caller.
- **Signed list over HTTP.** The visor's dmsg HTTP service (port 80, package
  `logserver`) serves `GET /transports` only to callers that reached the visor
  over a skywire transport. It returns 403 to other callers, 503 when the list
  is unavailable, 304 when `If-None-Match` equals the current `ETag`, and
  otherwise a `transport.SignedList` (`pk`, `at`, `entries` as `CompactEntry`,
  `sig`). The signature is by the visor key over the owner, the build time and
  the entries. `SignedList.Version` names the set and is the `ETag`. The visor
  rebuilds the list at most every 5 seconds (`transportListTTL`) and excludes
  closed transports and transports labelled `setup`.
- **Local history.** `GET /stats/transports` and `/stats/transports/history` on
  the same HTTP service return the visor's transport records with first seen,
  last seen and daily rollups. They are limited to the survey whitelist when one
  is configured.

## Security Considerations

Registration is authenticated by the visor key, either the HTTP signature or
the CXO root signature, and TPD accepts only entries that name the reporter as
an edge. Entries are not signed by both edges, so a visor can register a
transport to a peer that does not hold it; the entry expires unless refreshed.
The public routes carry no authentication and are rate limited per caller. The
deregistration route is limited to the deregistration whitelist. The transport query
is authorised by a route setup node signature bound to one target and one
requester. Visor feeds are limited to the peer whitelist and the TPD.

## References

- [02 HTTP Authorization Middleware](02-HTTP_Authorization_Middleware.md)
- [13 Setup Node](13-Setup_Node.md)
- [31 CXO Feeds](31-CXO_Feeds.md)
