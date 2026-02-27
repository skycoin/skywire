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
- `/pkg/route-finder/api/` contains the RESTFUL API definitions.
- `/pkg/route-finder/store/` contains graph building and route finding logic.
- `/pkg/route-finder/client/` contains the client library that interacts with the *Route Finder* service's RESTFUL API.

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

```json
{
    "edges": [
        ["<source-public-key>", "<destination-public-key>"],
        ["<source-public-key-2>", "<destination-public-key-2>"]
    ],
    "opts": {
        "min_hops": 0,
        "max_hops": 16
    }
}
```

**Request Fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `edges` | array | Yes | Array of [source, destination] public key pairs |
| `opts` | object | No | Route filtering options |
| `opts.min_hops` | integer | No | Minimum number of hops (default: 0) |
| `opts.max_hops` | integer | No | Maximum number of hops (default: 0, meaning no limit) |

**Responses:**

- 200 OK (Success).
    ```json
    {
        "<source-pk>:<destination-pk>": [
            [
                {
                    "tp_id": "<transport-id-uuid>",
                    "from": "<source-visor-pk>",
                    "to": "<next-hop-visor-pk>"
                },
                {
                    "tp_id": "<transport-id-uuid>",
                    "from": "<next-hop-visor-pk>",
                    "to": "<destination-visor-pk>"
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
| `tp_id` | string (UUID) | Transport ID connecting the two nodes |
| `from` | string | Source visor public key (hex) |
| `to` | string | Destination visor public key (hex) |

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
