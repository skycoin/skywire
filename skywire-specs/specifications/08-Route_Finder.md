# Route Finder

The *Route Finder* (or *Route Finding Service*) is responsible for finding and suggesting routes between two Skywire Nodes (identified by public keys). It is expected that an *App Node* is to use this service to find possible routes before contacting the *Setup Node*.

The *Route Finder* uses a Breadth-First Search (BFS) algorithm to find the shortest paths and returns up to 3 routes per source-destination pair, sorted by hop count (ascending).

## Graph Algorithm

In order to explore routing we need to create a graph that represents the current skywire network, or at least the network formed by all the reachable nodes from the `source node`.

For this purpose, we use a Depth-First Search (DFS) algorithm to build the network graph from a root node. The graph construction:

1. Starts from the source node
2. Queries the Transport Discovery for all transports connected to each node
3. Recursively explores connected nodes until all reachable nodes are discovered
4. Only considers nodes reachable from the source node

## Routing Algorithm

Given the network graph, the Route Finder uses BFS to find paths from source to destination:

1. **Path Finding**: Finds all paths from source to destination visor
2. **Hop Constraints**: Returns paths with hops in range [minHops, maxHops]
3. **Cycle Prevention**: Ensures no duplicate vertices in a single path
4. **Sorting**: Routes sorted by hop count (ascending - shortest first)
5. **Limit**: Returns maximum 3 routes per edge pair

## Code Structure

The code should be in the `skycoin/skywire` repository:

- `/cmd/route-finder/route-finder.go` is the main executable for the *Route Finder*.
- `/pkg/deployment/rf/api/` contains the RESTFUL API definitions.
- `/pkg/deployment/rf/store/` contains graph building and route finding logic.
- `/pkg/deployment/rf/client/` contains the client library that interacts with the *Route Finder* service's RESTFUL API.

## Database

The *Route Finder* accesses the Transport Discovery database to build its network graph. It does not maintain its own persistent storage.

## Endpoint Definitions

All endpoint calls should include an `Accept: application/json` field in the request header, and the response header should include a `Content-Type: application/json` field.

### POST Find Routes

Finds routes between one or more source-destination pairs.

**Request:**

```
POST /routes
Content-Type: application/json
```

> The request and response structs carry no `json` struct tags (except
> `Latency`), so the wire keys are the Go field names verbatim. Two
> consequences, and they differ by direction:
>
> - **Responses** are emitted capitalised: `TpID`, `From`, `To`. A client
>   reading `tp_id` finds nothing.
> - **Requests** are decoded case-insensitively, so `edges` and `opts` do
>   match. But `min_hops` and `max_hops` do NOT — case-folding does not
>   bridge an underscore. Those options are dropped silently and the
>   request still succeeds, so a hop-constrained query comes back
>   unconstrained with no error to indicate why.
>
> Use the exact keys below in both directions.

```json
{
    "Edges": [
        ["<source-public-key>", "<destination-public-key>"],
        ["<source-public-key-2>", "<destination-public-key-2>"]
    ],
    "Opts": {
        "MinHops": 0,
        "MaxHops": 16,
        "NumRoutes": 0
    }
}
```

**Request Fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `Edges` | array | Yes | Array of [source, destination] public key pairs |
| `Opts` | object | No | Route filtering options |
| `Opts.MinHops` | integer | No | Minimum number of hops (default: 0) |
| `Opts.MaxHops` | integer | No | Maximum number of hops (default: 0, meaning no limit) |
| `Opts.NumRoutes` | integer | No | Desired number of distinct routes per edge. Zero means the service default. A multiplexed dial MUST set this to its mux degree (plus headroom); otherwise the finder caps at 3 routes and a higher requested mux degree silently degrades. |

**Responses:**

- 200 OK (Success).
    ```json
    {
        "<source-pk>:<destination-pk>": [
            [
                {
                    "TpID": "<transport-id-uuid>",
                    "From": "<source-visor-pk>",
                    "To": "<next-hop-visor-pk>",
                    "Latency": 12.5
                },
                {
                    "TpID": "<transport-id-uuid>",
                    "From": "<next-hop-visor-pk>",
                    "To": "<destination-visor-pk>"
                }
            ]
        ]
    }
    ```

**Response Structure:**

The response is a JSON object where:
- **Key**: Path edges serialized as `"source-pk:destination-pk"`
- **Value**: Array of routes, where each route is an array of Hop objects

**Hop Object:**

| Field | Type | Description |
|-------|------|-------------|
| `TpID` | string (UUID) | Transport ID connecting the two nodes |
| `From` | string | Source visor public key (hex) |
| `To` | string | Destination visor public key (hex) |
| `Latency` | number | Measured transport latency for this hop, in milliseconds. Informational only — it is NOT used in route-rule setup. Omitted when the edge has no measurement, and by route-finders that predate the field. |

**Error Responses:**

- 400 Bad Request (Malformed request, invalid public keys).
    ```json
    {
        "error": {
            "code": 400,
            "message": "invalid public key format"
        }
    }
    ```
- 404 Not Found (No route exists between nodes).
    ```json
    {
        "error": {
            "code": 404,
            "message": "no route to destination"
        }
    }
    ```
- 500 Internal Server Error (Server error).
    ```json
    {
        "error": {
            "code": 500,
            "message": "internal server error"
        }
    }
    ```

### GET Health Check

Returns service health information.

**Request:**

```
GET /health
```

**Response:**

- 200 OK (Success).
    ```json
    {
        "build_info": {
            "version": "v1.0.0",
            "commit": "abc123def456",
            "date": "2024-02-25T10:30:00Z"
        },
        "started_at": "2024-02-25T10:00:00Z",
        "dmsg_address": "02abc123:9000",
        "dmsg_servers": ["02def456:8001", "02ghi789:8001"]
    }
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `build_info` | object | Build metadata (optional) |
| `build_info.version` | string | Application version |
| `build_info.commit` | string | Git commit hash |
| `build_info.date` | string | Build date (RFC3339) |
| `started_at` | string | Server start time (RFC3339) |
| `dmsg_address` | string | DMSG address of this service (optional) |
| `dmsg_servers` | array | List of DMSG server public keys (optional) |

---

## Endpoint Summary

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/routes` | POST | No | Find routes between node pairs |
| `/health` | GET | No | Health check |
