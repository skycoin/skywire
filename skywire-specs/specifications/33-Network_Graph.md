# Network Graph

| Status   | Implemented                                                                      |
|----------|----------------------------------------------------------------------------------|
| Revision | 2026-10-27, skywire 38152beed                                                    |
| Code     | `pkg/deployment/netgraph`, `pkg/deployment/tpd/api/charts.go`, `pkg/tpviz/netview` |

## Abstract

The network graph is a web page that shows the transports of the network as a
graph of visors. The package `pkg/deployment/netgraph` lays out the graph and
renders the page. Transport Discovery (TPD) serves it at `/graph` and supplies
the transports.

## Overview

The graph is not a separate service. `netgraph.Page` is an `http.Handler` that
the TPD mounts beside its charts page. The page calls back into TPD for its
data: the transports, a color for some visors, and the grouping of visors. The
page is drawn in the browser by cosmos-go, a WebGL engine in the `netview` module.

## Addressing

The page is served by the TPD over dmsg HTTP at `dmsg://<tpd-pk>:80`, under the
paths in the table below. It exists only while the TPD charts are running.
Otherwise the paths answer 404 (`API.GraphPage`).

## Data Formats

### Inputs

`netgraph.Page` takes these functions.

| Field    | Signature                            | TPD value                               |
|----------|--------------------------------------|-----------------------------------------|
| `Source` | `func(ctx) ([]Link, error)`          | `API.graphLinks`: every transport in the TPD transport cache, including transports between a visor and itself |
| `Marks`  | `func(ctx) map[string]string`        | `API.roleColors`: a color for each visor that runs a dmsg server |
| `Groups` | `func(ctx) map[string]string`        | `API.ipGroups`: one key for visors that share a public IP |

`netgraph.Link` has the two visor keys in hex, `A` and `B`, and the transport
type `Type`. `netgraph.Mark` is a legend entry with a name and a color.

The TPD takes the roles from dmsg discovery (the clients by server CXO feed, or
`/dmsg-discovery/all_servers` and `/dmsg-discovery/servers/clients` over HTTP). A
server with an entry in discovery is colored as a registered dmsg server. A
visor that clients delegate to without an entry is colored as a LAN dmsg server.
The roles are refreshed every 10 minutes (`dmsgRolesEvery`).

`ipGroups` joins visors that share a public IP in the Address Resolver bindings
feed (see Address Resolver). The IPs are the public addresses of `stcpr`, `sudph`,
`squicr` and `swtr` bindings.

### Graph

`netgraph.Build` produces a `netgraph.Graph`. A node is a visor key. An edge is a
pair of visors joined by at least one transport. An edge holds a mask with one bit
for each transport type that joins the pair. Types are ordered by transport count,
at most 8 are kept (`maxTypes`), and the rarest share the type `other`.
A graph with more than 65535 nodes is empty.

Visors that share a group key are numbered as a group when two or more of them
appear in the graph. Group numbers hide the keys.

## Interfaces

### HTTP API

None of these routes requires authentication.

| Method and path         | Response                                                   |
|-------------------------|------------------------------------------------------------|
| `GET /graph`            | the HTML page; 503 until the transports are known; 405 for methods other than `GET` and `HEAD` |
| `GET /graph/engine.wasm`| the `netview` WebAssembly module                           |
| `GET /graph/engine.js`  | the `wasm_exec.js` loader of the module                    |

The page response has `Cache-Control: public, max-age=300` and a restrictive
`Content-Security-Policy` header.

The page embeds its data as JSON with these members: `t` the type names, `k` the
visor keys, `x` and `y` the positions, `d` the transport count of each visor, `c`
the marked colors, `g` the group numbers and `e` the edges. The edges are packed
into five bytes each: two little endian 16 bit node indexes and the type mask. The
field is base64 encoded.

## Behaviour

A layout is kept for `RefreshEvery`, 15 minutes. After that the next request
recomputes it from the transports of the TPD cache (refreshed every 30 s,
`transportsCacheDelay`). If recomputation fails, the previous page is served.

The layout is a force directed layout in a unit square. Every pair of visors repels
and every edge pulls its ends together. Gravity centers the graph, and visors of a
group are drawn together. The pull on an edge falls with the degree of the visor.
The layout runs 220 iterations from a spiral start. If more than 90 percent of the
visors have a position from the previous layout it starts from those positions and
runs 60 iterations.

## Security Considerations

The page lists the public keys of all visors that have a transport. The groups
view shows group numbers and sizes and not the shared IP addresses.

## References

- Transport Discovery
- Address Resolver
