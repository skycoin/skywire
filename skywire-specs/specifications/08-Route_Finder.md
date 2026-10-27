# Route Finder

| Status   | Implemented                                                                       |
|----------|-----------------------------------------------------------------------------------|
| Revision | 2026-10-27, skywire 38152beed                                                     |
| Code     | `pkg/deployment/rf`, `pkg/services/rf`, `pkg/rfclient`, `pkg/router`, `pkg/visor` |

## Abstract

The Route Finder answers queries for routes between two visors. It holds the
network transport graph built from the Transport Discovery transport set and
returns the lowest latency paths that fit the requested hop limits. A visor can
also calculate routes itself, without the Route Finder, from its own transports
and a snapshot of the network graph.

## Overview

The router asks for routes when it dials a remote visor (see Router and Route
Setup). The route finder is one of several route sources. The others are the
direct transport check, the RSN oracle for two hop routes, the neighbour
transport lists for three hop routes, and local calculation described below.

The Route Finder reads transports from the Transport Discovery store. It keeps
no storage of its own. When it runs in the same process as Transport Discovery
it builds its graph from that service's live transport set instead of reading
Redis (`GraphCache.ShareFrom`).

## Addressing

The Route Finder is served over dmsg HTTP at `dmsg://<route-finder-pk>:80`.
The address is listed in the deployment configuration (`deployment.Services`).

## Data Formats

### Request

`rfclient.FindRoutesRequest` has no JSON tags, so the keys are the Go field
names. The decoder matches keys case-insensitively, but an underscore is not
folded, so `min_hops` is not `MinHops`.

| JSON key | Type                      | Meaning                          |
|----------|---------------------------|----------------------------------|
| `Edges`  | array of `[src, dst]` keys | `routing.PathEdges`, one entry per query |
| `Opts`   | object, OPTIONAL          | `rfclient.RouteOptions`          |

`rfclient.RouteOptions`:

| JSON key    | Type   | Meaning                                              |
|-------------|--------|------------------------------------------------------|
| `MinHops`   | uint16 | minimum hop count, 0 for no minimum                  |
| `MaxHops`   | uint16 | maximum hop count, 0 for no maximum                  |
| `NumRoutes` | uint16 | routes wanted per edge pair, 0 selects the default   |

### Response

On success the body is a JSON object. Each key is the text form of a
`routing.PathEdges`, `<src-pk>:<dst-pk>`. Each value is an array of routes,
ordered best first. A route is an array of `routing.Hop`.

| JSON key        | Type   | Meaning                                              |
|-----------------|--------|------------------------------------------------------|
| `TpID`          | UUID   | transport ID                                         |
| `From`, `To`    | string | public keys of the hop ends                          |
| `Latency`       | float  | measured latency in ms, omitted when unmeasured      |
| `Type`          | string | transport type, omitted when empty                   |
| `ThroughputBps` | float  | observed throughput, omitted when zero               |

Errors use `rfclient.HTTPResponse` with `{"error":{"message":..., "code":...}}`
(`rfclient.HTTPError`).

## Interfaces

### HTTP API

No endpoint requires authentication. A request body is limited to 1 MiB
(`maxBodyBytes`). Responses larger than a small threshold are gzip compressed
(`httputil.CompressMin`).

| Method and path | Purpose                              |
|-----------------|--------------------------------------|
| `POST /routes`  | find routes for each pair in `Edges` |
| `GET /health`   | `httputil.HealthCheckResponse` with service name `route-finder` |
| `GET /`         | status page with charts              |

`POST /routes` status codes:

| Code | Condition                                                          |
|------|--------------------------------------------------------------------|
| 200  | routes found for every pair                                        |
| 400  | the body cannot be read or is not valid JSON                       |
| 404  | the source has no transports, or no route exists for some pair     |
| 500  | the per-source graph build failed with another error               |

`rfclient` maps a 404 to `rfclient.ErrTransportNotFound`.

## Behaviour

### Graph

`GraphCache` holds one graph of the whole network, shared by all requests.
`Rebuild` reads the transport set once, with latency and throughput overlaid
when the store supports it, and links the entries into vertices.

| Constant                    | Value | Meaning                                         |
|-----------------------------|-------|-------------------------------------------------|
| `DefaultCacheRefresh`       | 15 s  | age after which a request triggers a rebuild    |
| `staleFactor`               | 4     | at this many refresh intervals a request waits for the rebuild |

A request that finds the graph older than the refresh interval is answered from
it while one rebuild runs in the background. Before the first build completes,
each request builds a graph from its source with depth limited to
`MaxHops`, or 10 when `MaxHops` is 0 or above 10.

The graph excludes transports labelled `transport.LabelSetup`, and transports
of type `local` (`transport.Routable`). When several transports join the same
two visors, the one with the lowest `tptypes.TypePreference` is kept. A `dmsg`
transport may only be the last hop of a route.

### Route count

| Constant           | Value | Meaning                                  |
|--------------------|-------|------------------------------------------|
| `maxNumberOfRoutes`| 3     | routes per pair when `NumRoutes` is 0    |
| `routesCeiling`    | 20    | upper bound on `NumRoutes`               |

### Ranking

`Graph.GetRouteWeighted` returns the routes with the lowest total latency. A
route's latency is the sum of its hops' measured latency. A hop without a
measurement counts `HopLatencyPenaltyMS`, 500 ms.

A breadth-first search (`StreamRoutesWithCap`) emits paths in increasing hop
count, avoids repeated visors in a path and stops expanding when the queue holds
`DefaultMaxBFSQueue`, 200000, items. The search collects a candidate pool of
`NumRoutes * 32` routes, bounded to the range 128 to 2048, and sorts the pool by
latency. Results are memoised per graph. Failures are not cached.

### Landmark routing

Landmark routing is on unless the `disable_landmark_routing` configuration
field (`rf.Config.DisableLandmarkRouting`) is set. For each pair the finder
first runs a bounded search to depth `landmarkDirectDepth`, 3, with a queue
budget of `landmarkBFSBudget`, 40000. If that yields `NumRoutes` routes they are
returned. Otherwise it composes routes from the source to a hub and from the hub
to the destination and ranks them together with the bounded results. If both are
empty the exhaustive search runs.

| Constant                | Value | Meaning                                   |
|-------------------------|-------|-------------------------------------------|
| `defaultLandmarkHubs`   | 8     | highest degree visors used as hubs        |
| `defaultLandmarkK`      | 5     | routes stored per visor and hub           |
| `defaultLandmarkMaxLen` | 6     | hop limit of a stored sub-route           |
| `landmarkMaxAge`        | 2 min | age until hub tables are rebuilt rather than inherited |

`dmsg` transports are excluded from the hub tables.

## Local Route Calculation

A visor can compute routes without the Route Finder. `router.calculateLocalRoutes`
returns a forward and a reverse path. It runs in this order.

1. It collects the visor's own open transports, skipping closed ones and those
   labelled `transport.LabelSetup`, and orders them by type preference.
2. When the minimum hop setting allows one hop, it returns the first transport
   whose remote end is the destination. `dmsg` is skipped when the dial excludes
   it, and so is any transport named by the dial's exclusions.
3. A dial from a visor to itself looks for a two hop loop through a transport
   partner.
4. Otherwise it runs `localRouteBFS`. The search is seeded with the local
   non-`dmsg` transports and expands over a network graph snapshot indexed by
   visor. It visits levels in increasing hop count and returns at the first
   level that reaches the destination between the minimum and `MaxHops`, 7 when
   the router configuration leaves it 0. Among the hits of that level it returns
   the path with the lowest `pathLatencyScore`. Expansion is ordered by public
   key, so the same inputs give the same path. `dmsg` edges, intermediates named
   in the dial's exclusions, and repeated visors in a path are skipped.
   The reverse path is the forward path reversed.

Steps 4 and the self loop need the network graph, which is held only when the
router setting `dial.local_graph` (`routersettings.DialLocalGraph`) is on. Its
default is off. With it off, `calculateLocalRoutes` returns `errLocalGraphOff`
for a destination without a direct transport.

### Network graph snapshot

The snapshot is the discovery client's `GetAllTransports` result.
On a visor the client is `cxoAwareTPD`. It assembles the list from the
Transport Discovery routing CXO feed (`FeedTPDRouting`, dmsg port 75, see
Transport Discovery) by `routingfeed.Reader`. Until that feed has synced it
reports `ErrTPDAllTransportsNotReady`. The first use pins the feed
subscription.

`tpdSnapshotCache` holds one immutable snapshot with lookups by visor and by
transport ID for latency, type and throughput. It is replaced when the feed's
last sync time advances. A client without a sync time uses a TTL of
`defaultTPDSnapshotTTL`, 5 minutes. A stale snapshot is served while one
background refresh, bounded by 2 minutes, rebuilds it. Only a cold cache makes a
caller wait.

### Local graph source

A visor may install a `LocalGraphSource` (`Visor.SetLocalGraphSource`), a
function that returns extra transport entries and a version time. A hypervisor
installs `Hypervisor.AttachedTransportEntries`, which builds entries from the
transports in the cached summaries of its attached visors. A summary older than
`attachedGraphStaleAfter`, twice the hypervisor poll interval, is dropped. Setup
transports and entries with an empty end are dropped.

`cxoAwareTPD.GetAllTransports` merges the source into the network list with
`mergeTransportEntries`. An entry whose ID is already in the network list is
not replaced. When the routing feed is not ready, the local entries alone are
returned if there are any. The version reported to the snapshot cache is the
later of the feed time and the source version.

### Choosing between local calculation and the Route Finder

`router.fetchBestRoutes` selects the source in this order.

1. When force local routes is set, `calculateLocalRoutes` is the only source. A
   failure is returned and the dial makes one fetch attempt. The setting is
   changed with `Visor.SetForceLocalRoutes` and `Router.SetForceLocalRoutes`.
2. A one hop route over an existing transport is used when the hop limits allow
   one hop.
3. For a minimum of at most 2 hops, the RSN oracle is tried. For at most 3
   hops, the neighbours' transport lists are tried.
4. When the destination is an edge in the local graph source
   (`Visor.localGraphHasPeer`, set as `PreferLocalRouteTo`), local calculation
   runs before the Route Finder.
5. The Route Finder is queried for both directions. One query covers both when
   the minimum hops are equal. Otherwise there is one query per direction.
   `NumRoutes` is the mux degree plus headroom.
6. If the Route Finder answers `rfclient.ErrTransportNotFound`, or its retries
   are exhausted, local calculation runs as a fallback.

## Security Considerations

The Route Finder does not authenticate callers. It serves public topology data
already published by Transport Discovery.

## References

- Transport Discovery
- Router
- Route Setup Process
