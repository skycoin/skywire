# Transport Discovery

The Transport Discovery is a service that exposes a RESTful interface and interacts with a database on the back-end.

The database stores *Transport Entries* that can be queried using their *Transport ID* or via a given *Transport Edge*.

The process of submitting a *Transport Entry* is called *Registration* and a Transport cannot be deregistered. However, nodes that are an *Edge* of a *Transport*, can update their *Transport Status*, and specify whether the *Transport* is up or down.

Any state-altering RESTful call to the *Transport Discovery* is authenticated using signatures, and replay attacks are avoided by expecting an incrementing security nonce (all communication should be encrypted with HTTPS anyhow).

## Transport Discovery Procedures

This is a summary of the procedures that the *Transport Discovery* is to handle.

**Registering a Transport:**

Technically, *Transports* are created by the Skywire Nodes themselves via an internal *Transport Factory* implementation. The *Transport Discovery* is only responsible for registering *Transports* in the form of a *Transport Entry*.

When two Skywire Nodes establish a Transport connection between them, it is at first, unregistered in the *Transport Discovery*. The node that initiated the creation of the Transport (or the node that called the `(transport.Transport).Dial` method), is the node that is responsible for initiating the *Transport Settlement Handshake*.

If two nodes; **A** and **B** establish a *Transport* between them (where **A** is the *Transport Initiator*), **A** is then also responsible for sending the first handshake packet for the *Transport Settlement Handshake*. The procedure is as follows:

1. **A** sends **B** a proposed `transport.Entry` and also **A**'s signature of the Entry (in the form of `transport.SignedEntry`).

2. **B** checks the `transport.SignedEntry` sent from **A**;

   1. The `Entry.ID` field should be unique (check via *Transport Discovery*).
   2. The `Entry.Edges` field should be ordered correctly and contain public keys of **A** and **B**.
   3. The `Entry.Type` field should have the expected Transport Type.
   4. The `Signatures` field should contain **A**'s valid signature in the correct location (in the same index as **A**'s public key in `Entry.Edges`).
   5. The `Registered` field should be empty.

3. **B** then adds it's only signature to the `transport.SignedEntry` and registers it to the *Transport Discovery*. Both public and private Transports are registered in the *Transport Discovery* (however only public *Transports* are publicly available).

4. **B** then informs **A** on the success/failure of the registration, or just that the `transport.SignedEntry` is accepted by itself (depending on whether the Transport is to be public or not).

**Transport Status via Re-registration:**

Transport status is determined by the re-registration mechanism rather than explicit status updates:

- Visors re-register their transports every **90 seconds**
- Transport entries have a TTL of **2 minutes**
- A transport that is not re-registered within the TTL is considered *down* and expires from the registry
- Re-registration includes updated bandwidth data (cumulative bytes sent/received)

This approach simplifies the protocol and ensures transport status accurately reflects actual connectivity.

**Reporting Transport Bandwidth:**

Bandwidth data is reported automatically during transport re-registration. The `SignedEntry` includes:

```go
type BandwidthData struct {
    SentBytes uint64 // Total bytes sent (cumulative)
    RecvBytes uint64 // Total bytes received (cumulative)
}
```

Each edge reports its own perspective. The Transport Discovery stores both reports and can verify consistency between edges.

**Obtaining Transports:**

There are two ways to obtain transports; either via the assigned *Transport ID*, or via one of the *Transport Edges*. There is no restriction as who can access this information and results can be sorted by a given meta.

## Transport Types

The Transport Discovery supports the following transport types:

| Type | Name | Description |
|------|------|-------------|
| `stcpr` | STCP Resolved | TCP transport that resolves addresses using the address-resolver service. Used for connections where the remote visor's IP is not directly known. |
| `sudph` | SUDP Hole-punch | UDP transport that resolves addresses using address-resolver and establishes connections via UDP hole-punching. Enables direct peer-to-peer connections through NAT. |
| `stcp` | STCP | Direct TCP transport that resolves addresses using a local PK table. Typically used in local/private networks where IPs are known. |
| `dmsg` | DMSG | Transport that works through the DMSG intermediary network. Provides connectivity when direct connections are not possible. |

**Transport Type Selection:**

- `stcpr` is used for TCP connections where the address-resolver provides the remote visor's address
- `sudph` is preferred when UDP hole-punching can establish a direct connection (lower latency)
- `stcp` is used in local networks with a configured PK-to-IP table
- `dmsg` provides an alternative routing path through the DMSG overlay network

---

## Security Procedures

**Incrementing Security Nonce:**

An *Incrementing Security Nonce* is represented by a `uint64` value.

To avoid replay attacks and unauthorized access, each public key of a *Skywire Node* is assigned an *Incrementing Security Nonce*, and is expected to sign it with the rest of the body, and include the signature result in the http header. The *Incrementing Security Nonce* should increment every time an" endpoint is called (except for the endpoint that obtains the next expected incrementing security nonce). An *Incrementing Security Nonce* is not operation-specific, and increments every time any endpoint is called by the given Skywire Node.

The *Transport Discovery* should store a table of expected next *Incrementing Security Nonce* for each public key of a *Skywire Node*. There is an endpoint `GET /security/nonces/{public-key}` that provides the next expected *Incrementing Security Nonce* for a given Node public key. This endpoint should be publicly accessible, but nevertheless, the *Skywire Nodes* themselves should keep a copy of their next expected *Incrementing Security Nonce*.

The only times an *Incrementing Security Nonce* should not increment is when:

- An invalid request is submitted (missing/extra fields, invalid signature).
- An internal server error occurs.

Initially, the expected *Incrementing Security Nonce* should be 0. When it is this value, the *Transport Discovery* should not have an entry for it.

Each operation should contain the following extra header entries:

- `SW-Public` - Specifies the public key of the Skywire Node performing this operation.
- `SW-Nonce` - Specifies the incrementing nonce provided by this operation.
- `SW-Sig` - Specifies the hex-representation of the signature of the hash result of the concatenation of the *Incrementing Security Nonce* + Body of the request.

If these values are not valid, the *Transport Discovery* should reject the request.

## Code Structure

The code should be in the `skywire-services` repository.

- `/cmd/transport-discovery/transport-discovery.go` is the main executable for the *Transport Discovery*.
- `/pkg/transport-discovery/api/` contains the RESTFUL API definitions.
- `/pkg/transport-discovery/store/` contains the definition of the `Storer` interface and it's implementations.
- `/pkg/transport-discovery/client/` contains the client library that interacts with the *Transport Discovery* server's RESTFUL API.

## Database

The *Transport Discovery* should work with a variety of databases and the following interfaces should be defined for such implementations;

- `TransportStorer` should store *Transport Signed Entries* and it's associated *Transport Statuses*.
- `NonceStorer` should store expected *Incrementing Nonces*.

## Endpoint Definitions

The following endpoints are implemented in the Transport Discovery service. See the Endpoint Summary at the end of this document for a complete reference table.

**Core Endpoints (Authenticated):**
- `GET /security/nonces/{pk}` - Get security nonce
- `GET /transports/id:{id}` - Get transport by ID
- `GET /transports/edge:{pk}` - Get transports by edge
- `POST /transports/` - Register transport(s)
- `DELETE /transports/id:{id}` - Delete transport
- `POST /transports/delete-batch` - Delete multiple transports

**Public Endpoints:**
- `GET /transports/stats/{pk}` - Get transport stats for edge
- `GET /all-transports` - Get all transports
- `GET /all-transports/stats` - Get aggregate statistics
- `GET /all-transports/per-key-stats` - Get per-visor statistics
- `GET /uptimes` - Get visor uptime data
- `GET /metric` - Network-wide aggregate stats (supports `bandwidth`, `latency` params)
- `GET /metric/visor/{pks}` - Visor aggregate stats (accepts comma-separated PKs)
- `GET /metrics` - Per-transport metrics (supports `bandwidth`, `latency` params)
- `GET /metrics/{ids}` - Specific transport metrics (accepts comma-separated IDs)
- `GET /metrics/visor/{pks}` - Visor's transport metrics (accepts comma-separated PKs)
- `GET /health` - Health check

**Request Headers:**

All responses include `Content-Type: application/json`. Authenticated endpoints require the following headers:

```
Content-Type: application/json
SW-Public: <public-key>
SW-Nonce: <nonce>
SW-Sig: <signature>
```

| Header | Description |
|--------|-------------|
| `SW-Public` | The visor's public key (hex-encoded, 66 characters) |
| `SW-Nonce` | The incrementing security nonce for this request |
| `SW-Sig` | Hex-encoded signature of SHA256(nonce + request body) using the visor's secret key |

### GET Incrementing Security Nonce

Obtains the next expected incrementing nonce for a given edge's public key.

**Request:**

```
GET /security/nonces/{pk}
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `pk` | string | The visor's public key (hex-encoded, 66 characters) |

**Response:**

- 200 OK (Success).
    ```json
    {
        "edge": "<public-key>",
        "next_nonce": 0
    }
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `edge` | string | The public key that was queried |
| `next_nonce` | integer | The next expected nonce value for this public key. Starts at 0 for new keys. |

**Error Responses:**

- 400 Bad Request (Invalid public key format).
- 500 Internal Server Error (Server error).

### GET Transport Entry via Transport ID

Obtains a *Transport* via a given *Transport ID*. Returns a single transport entry.

**Request:**

```
GET /transports/id:{id}
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | string | Transport UUID (36 characters, e.g., `550e8400-e29b-41d4-a716-446655440000`) |

**Response:**

- 200 OK (Success).
    ```json
    {
        "t_id": "<transport-id>",
        "edges": [
            "<public-key-1>",
            "<public-key-2>"
        ],
        "type": "stcpr",
        "public": true
    }
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `t_id` | string | Transport UUID (see "Transport ID Generation" below) |
| `edges` | array | Array of two public keys representing the transport endpoints (visors), sorted by numeric value (least-significant first) |
| `type` | string | Transport type: `stcpr` (STCP Resolved), `sudph` (SUDP Hole-punch), or `dmsg` |
| `public` | boolean | Whether this transport is publicly visible in the registry |

**Transport ID Generation:**

The transport ID is deterministically generated from a SHA-256 hash of the transport's defining properties:

```
ID = UUID(SHA256(sorted_edge_1 || sorted_edge_2 || transport_type))
```

Where:
- `sorted_edge_1`, `sorted_edge_2`: The two public keys (33 bytes each), sorted by numeric value (least-significant first)
- `transport_type`: The transport type string (`stcpr`, `sudph`, `stcp`, or `dmsg`)

**Properties:**
- The same two visors with the same transport type will always produce the same transport ID
- The order of edges doesn't matter: `MakeTransportID(A, B, type) == MakeTransportID(B, A, type)`
- Different transport types between the same visors produce different IDs
- Given a transport ID and both edge public keys, the transport type can be determined by computing IDs for each known type and comparing

**Error Responses:**

- 400 Bad Request (Invalid transport ID format).
- 404 Not Found (Transport does not exist).
- 500 Internal Server Error (Server error).

### GET Transport(s) via Edge Public Key

Obtains all *Transports* where the given public key is one of the edges.

**Request:**

```
GET /transports/edge:{pk}
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `pk` | string | Visor public key (hex-encoded, 66 characters) |

**Response:**

- 200 OK (Success).
    ```json
    [
        {
            "t_id": "<transport-id-1>",
            "edges": [
                "<public-key-1>",
                "<public-key-2>"
            ],
            "type": "stcpr",
            "public": true
        },
        {
            "t_id": "<transport-id-2>",
            "edges": [
                "<public-key-1>",
                "<public-key-3>"
            ],
            "type": "sudph",
            "public": true
        }
    ]
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `t_id` | string | Transport UUID - unique identifier for this transport |
| `edges` | array | Array of two public keys representing the transport endpoints |
| `type` | string | Transport type: `stcpr`, `sudph`, or `dmsg` |
| `public` | boolean | Whether this transport is publicly visible |

**Error Responses:**

- 400 Bad Request (Invalid public key format).
- 404 Not Found (No transports found for this edge).
- 500 Internal Server Error (Server error).

### GET Transport Stats by Edge

Returns transport statistics for a specific edge (visor). This provides a count breakdown without returning full transport entries.

**Request:**

```
GET /transports/stats/{pk}
```

**Response:**

- 200 OK (Success).
    ```json
    {
        "total": 15,
        "by_type": {
            "stcpr": 2,
            "sudph": 13
        }
    }
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `total` | integer | Total number of transports for this edge |
| `by_type` | object | Map of transport type to count |

- 400 Bad Request (Invalid public key).
- 404 Not Found (No transports found).
- 500 Internal Server Error (Server error).

### POST Register Transport(s)

Registers one or multiple Transports. This endpoint is also used for re-registration, which updates the transport's TTL and bandwidth data.

**Request:**

```
POST /transports/
POST /transports/?sync=true
```

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `sync` | boolean | false | When `true`, the response contains all registered transports instead of just the submitted entries. Used for local route calculation. |

**TPD Data Sync:**

When `sync=true` is specified, the Transport Discovery returns all registered transports in the response instead of echoing back the submitted entries. This enables visors to cache the full transport graph locally for:

- **Local route calculation** - Computing routes without querying the Route Finder service
- **Reduced API calls** - Obtaining transport data as a side-effect of periodic re-registration
- **Offline route planning** - Building routes when external services are unavailable

Visors with `sync_tpd_data` enabled in their configuration automatically use this parameter during the 90-second re-registration cycle.

**Request Body:**

```json
[
    {
        "entry": {
            "t_id": "<transport-id>",
            "edges": ["<public-key-1>", "<public-key-2>"],
            "type": "stcpr",
            "public": true
        },
        "signatures": ["<signature-1>", "<signature-2>"],
        "bandwidth": {
            "sent_bytes": 1048576,
            "recv_bytes": 2097152
        }
    }
]
```

**Request Fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `entry` | object | Yes | The transport entry to register |
| `entry.t_id` | string | Yes | Transport UUID (generated by the initiating visor) |
| `entry.edges` | array | Yes | Ordered array of two public keys [initiator, responder] |
| `entry.type` | string | Yes | Transport type: `stcpr`, `sudph`, or `dmsg` |
| `entry.public` | boolean | Yes | Whether this transport should be publicly visible |
| `signatures` | array | Yes | Array of two signatures, one from each edge, in same order as edges |
| `bandwidth` | object | No | Cumulative bandwidth data reported by this edge |
| `bandwidth.sent_bytes` | integer | No | Total bytes sent over this transport (cumulative) |
| `bandwidth.recv_bytes` | integer | No | Total bytes received over this transport (cumulative) |

**Response (default):**

- 201 Created (Success).
    ```json
    [
        {
            "entry": {
                "t_id": "<transport-id>",
                "edges": ["<public-key-1>", "<public-key-2>"],
                "type": "stcpr",
                "public": true
            },
            "signatures": ["<signature-1>", "<signature-2>"],
            "registered": 1708531200
        }
    ]
    ```

**Response (with `sync=true`):**

- 201 Created (Success).
    ```json
    [
        {
            "t_id": "<transport-id-1>",
            "edges": ["<public-key-1>", "<public-key-2>"],
            "type": "stcpr",
            "public": true
        },
        {
            "t_id": "<transport-id-2>",
            "edges": ["<public-key-3>", "<public-key-4>"],
            "type": "sudph",
            "public": true
        }
    ]
    ```

**Response Fields (default):**

| Field | Type | Description |
|-------|------|-------------|
| `entry` | object | The registered transport entry |
| `signatures` | array | The signatures provided during registration |
| `registered` | integer | Unix timestamp when the transport was first registered |

**Response Fields (with `sync=true`):**

| Field | Type | Description |
|-------|------|-------------|
| `t_id` | string | Transport UUID |
| `edges` | array | Array of two public keys (the transport endpoints) |
| `type` | string | Transport type: `stcpr`, `sudph`, or `dmsg` |
| `public` | boolean | Whether this transport is publicly visible |

**Note:** When `sync=true`, the response format matches the `/all-transports` endpoint, returning simplified transport entries without signatures or registration timestamps.

**Error Responses:**

- 400 Bad Request (Malformed request, invalid entry format).
- 401 Unauthorized (Invalid signature or nonce).
- 408 Request Timeout (Request took too long).
- 409 Conflict (Transport with same edges already exists).
- 500 Internal Server Error (Server error).

### DELETE Transport

Deletes a transport by ID. Only an edge of the transport can delete it.

**Request:**

```
DELETE /transports/id:{id}
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | string | Transport UUID to delete |

**Response:**

- 200 OK (Success).
    ```
    transport deleted
    ```

**Error Responses:**

- 400 Bad Request (Invalid transport ID format).
- 401 Unauthorized (Caller is not an edge of this transport, or invalid signature/nonce).
- 404 Not Found (Transport does not exist).
- 500 Internal Server Error (Server error).

### POST Delete Transports (Batch)

Deletes multiple transports in a single request. Only transports where the caller is an edge will be deleted; others are silently skipped.

**Request:**

```
POST /transports/delete-batch
```

**Request Body:**

```json
[
    "<transport-id-1>",
    "<transport-id-2>",
    "<transport-id-3>"
]
```

**Request Fields:**

| Field | Type | Description |
|-------|------|-------------|
| (array) | array of strings | List of transport UUIDs to delete |

**Response:**

- 200 OK (Success).
    ```json
    {
        "deleted": 2,
        "skipped": 1
    }
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `deleted` | integer | Number of transports successfully deleted |
| `skipped` | integer | Number of transports skipped (not found, not an edge, or invalid ID) |

**Error Responses:**

- 400 Bad Request (Request body is not a valid JSON array).
- 401 Unauthorized (Invalid signature/nonce).
- 500 Internal Server Error (Server error).

---

## Uptime Tracking Integration

The Transport Discovery integrates with uptime tracking to provide visor availability information. This endpoint mirrors the Uptime Tracker's data format for consistency.

### GET Uptimes

Returns visor uptime and online status. This endpoint caches and serves uptime tracker data, providing a unified view of visor availability alongside transport data.

**Note:** This endpoint does NOT include QoS metrics (bandwidth/latency). For QoS data, use the `/metrics` endpoints.

**Request:**

```
GET /uptimes
```

**Response:**

- 200 OK (Success).
    ```json
    [
        {
            "pk": "<public-key>",
            "on": true,
            "version": "v1.3.34",
            "daily": {
                "2024-02-19": "95.50",
                "2024-02-20": "100.00",
                "2024-02-21": "87.25"
            }
        }
    ]
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `pk` | string | Visor public key |
| `on` | boolean | Current online status |
| `version` | string | Skywire version running on the visor |
| `daily` | object | Map of date (YYYY-MM-DD) to uptime percentage |

---

## Extended Endpoints

The following endpoints extend the core Transport Discovery functionality with network visibility and quality-of-service metrics.

### GET All Transports

Returns all registered public transports. This endpoint provides the core transport registry data only, without QoS metrics.

**Request:**

```
GET /all-transports
GET /all-transports?selfTransports=hide
```

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `selfTransports` | string | (show all) | Set to `hide` to exclude self-transports (where both edges are the same visor) |

**Response:**

- 200 OK (Success).
    ```json
    [
        {
            "t_id": "<transport-id>",
            "edges": [
                "<public-key-1>",
                "<public-key-2>"
            ],
            "type": "stcpr",
            "public": true
        }
    ]
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `t_id` | string | Transport UUID |
| `edges` | array | Array of two public keys (the transport endpoints) |
| `type` | string | Transport type: `stcpr`, `sudph`, or `dmsg` |
| `public` | boolean | Whether this transport is publicly visible |

**Error Responses:**

- 404 Not Found (No transports registered).
- 500 Internal Server Error (Server error).

### GET All Transports Stats

Returns aggregate statistics about all transports in the network.

**Request:**

```
GET /all-transports/stats
GET /all-transports/stats?selfTransports=hide
```

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `selfTransports` | string | (show all) | Set to `hide` to exclude self-transports from counts |

**Response:**

- 200 OK (Success).
    ```json
    {
        "total_transports": 1500,
        "by_type": {
            "stcpr": 450,
            "sudph": 1050
        },
        "unique_visors": 320
    }
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `total_transports` | integer | Total number of registered transports |
| `by_type` | object | Map of transport type to count |
| `by_type.stcpr` | integer | Number of STCP Resolved transports |
| `by_type.sudph` | integer | Number of SUDP Hole-punch transports |
| `by_type.dmsg` | integer | Number of DMSG transports (if any) |
| `unique_visors` | integer | Number of unique visor public keys across all transports |

**Error Responses:**

- 500 Internal Server Error (Server error).

### GET Per-Key Stats

Returns transport counts per visor public key. Useful for finding well-connected visors.

**Request:**

```
GET /all-transports/per-key-stats
GET /all-transports/per-key-stats?selfTransports=hide
```

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `selfTransports` | string | (show all) | Set to `hide` to exclude self-transports from counts |

**Response:**

- 200 OK (Success).
    ```json
    {
        "<public-key-1>": {
            "total": 15,
            "stcpr": 2,
            "sudph": 13
        },
        "<public-key-2>": {
            "total": 8,
            "stcpr": 1,
            "sudph": 7
        }
    }
    ```

**Response Fields:**

The response is a map where each key is a visor public key (hex-encoded), and the value is an object containing:

| Field | Type | Description |
|-------|------|-------------|
| `total` | integer | Total number of transports for this visor |
| `stcpr` | integer | Number of STCP Resolved transports (if any) |
| `sudph` | integer | Number of SUDP Hole-punch transports (if any) |
| `dmsg` | integer | Number of DMSG transports (if any) |

**Note:** Only transport types that have a count > 0 are included in the response for each visor.

**Error Responses:**

- 500 Internal Server Error (Server error).

---

## Metrics

Metrics data (bandwidth and latency) is collected during transport re-registration and stored separately from core transport data.

### Reporting Configuration

| Parameter | Default Value | Description |
|-----------|---------------|-------------|
| Transport re-registration interval | 90 seconds | How often visors re-register their transports |
| Transport entry TTL | 2 minutes | Time before an unrefreshed transport is considered stale |
| Daily metrics retention | 35 days | How long daily aggregates are kept |

### Bandwidth Semantics

Bandwidth values are **cumulative** - they represent total bytes sent/received since the transport was established. Each transport edge reports its own perspective during re-registration. Both edges' reports are returned in the response (`a` and `b` fields), allowing clients to compare and verify consistency if needed.

### Latency Semantics

Latency values are in **milliseconds**. Each transport edge reports its own measured latency to the peer. Unlike bandwidth (cumulative), latency is reported as averages over the reporting period. Both edges' reports are returned in the response (`a` and `b` fields), allowing clients to compare or average as needed.

---

### GET /metric

Returns network-wide aggregate statistics (bandwidth and/or latency), broken down by transport type. Use `bandwidth=false` or `latency=false` to exclude specific metrics.

**Request:**

```
GET /metric
GET /metric?days=7
GET /metric?bandwidth=false
GET /metric?latency=false
```

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `days` | integer | 0 | Number of days of history (0 = all available, max 35) |
| `bandwidth` | boolean | true | Include bandwidth data in response |
| `latency` | boolean | true | Include latency data in response |

**Response:**

```json
{
    "daily": [
        {
            "date": "2024-02-21",
            "bandwidth": 53687091200,
            "latency": 45.2,
            "by_type": {
                "stcpr": { "bandwidth": 32212254720, "latency": 42.1 },
                "sudph": { "bandwidth": 21474836480, "latency": 48.3 }
            }
        }
    ],
    "cumulative": {
        "bandwidth": 1879948193280,
        "latency": 42.8,
        "by_type": {
            "stcpr": { "bandwidth": 1127968915968, "latency": 40.5 },
            "sudph": { "bandwidth": 751979277312, "latency": 45.1 }
        }
    }
}
```

**Response with `latency=false`:**

```json
{
    "daily": [
        {
            "date": "2024-02-21",
            "bandwidth": 53687091200,
            "by_type": {
                "stcpr": 32212254720,
                "sudph": 21474836480
            }
        }
    ],
    "cumulative": {
        "bandwidth": 1879948193280,
        "by_type": {
            "stcpr": 1127968915968,
            "sudph": 751979277312
        }
    }
}
```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `daily` | array | Per-day statistics |
| `daily[].date` | string | Date (YYYY-MM-DD) |
| `bandwidth` | integer | (When enabled) Total bytes |
| `latency` | number | (When enabled) Average latency in milliseconds |
| `by_type` | object | Breakdown by transport type |
| `cumulative` | object | Sum/average across all days in response |

---

## Per-Transport Metrics

The `/metrics` endpoints provide per-transport bandwidth and latency data. Use `bandwidth=false` or `latency=false` query parameters to exclude specific metrics from the response.

---

### GET /metrics

Returns per-transport metrics (bandwidth and/or latency) for all transports. Includes historical data for expired transports by default. Use `bandwidth=false` or `latency=false` to exclude specific metrics.

**Request:**

```
GET /metrics
GET /metrics?days=7
GET /metrics?type=stcpr
GET /metrics?live=true
GET /metrics?edges=true
GET /metrics?bandwidth=false
GET /metrics?latency=false
```

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `days` | integer | 0 | Number of days of history (0 = all available, max 35) |
| `type` | string | (all) | Filter by transport type: `stcpr`, `sudph`, `dmsg`, `stcp` |
| `live` | string | `all` | Filter by transport liveness: `true` (only live), `false` (only expired), `all` (both) |
| `edges` | boolean | false | Include actual public keys in response |
| `bandwidth` | boolean | true | Include bandwidth data in response |
| `latency` | boolean | true | Include latency data in response |

**Response:**

```json
[
    {
        "id": "<transport-id>",
        "type": "stcpr",
        "live": true,
        "daily": [
            {
                "date": "2024-02-21",
                "a": {
                    "bandwidth": { "sent": 536870912, "recv": 268435456 },
                    "latency": { "avg_ms": 45.2, "min_ms": 12.0, "max_ms": 120.5 }
                },
                "b": {
                    "bandwidth": { "sent": 268435456, "recv": 536870912 },
                    "latency": { "avg_ms": 44.8, "min_ms": 11.5, "max_ms": 118.0 }
                }
            }
        ]
    }
]
```

**Response with `latency=false` (bandwidth only):**

```json
[
    {
        "id": "<transport-id>",
        "type": "stcpr",
        "live": true,
        "daily": [
            {
                "date": "2024-02-21",
                "a": { "sent": 536870912, "recv": 268435456 },
                "b": { "sent": 268435456, "recv": 536870912 }
            }
        ]
    }
]
```

**Response with `bandwidth=false` (latency only):**

```json
[
    {
        "id": "<transport-id>",
        "type": "stcpr",
        "live": true,
        "daily": [
            {
                "date": "2024-02-21",
                "a": { "avg_ms": 45.2, "min_ms": 12.0, "max_ms": 120.5 },
                "b": { "avg_ms": 44.8, "min_ms": 11.5, "max_ms": 118.0 }
            }
        ]
    }
]
```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Transport UUID |
| `type` | string | Transport type (`stcpr`, `sudph`, `dmsg`, `stcp`) |
| `live` | boolean | Whether the transport is currently registered (true) or expired (false) |
| `edges` | array | (Only when `edges=true`) Array of two public keys [lower PK, higher PK] |
| `daily` | array | Per-day metrics data |
| `daily[].date` | string | Date (YYYY-MM-DD) |
| `a` | object | Metrics reported by first edge (lower PK) |
| `b` | object | Metrics reported by second edge (higher PK) |
| `bandwidth.sent` | integer | (When bandwidth enabled) Cumulative bytes sent |
| `bandwidth.recv` | integer | (When bandwidth enabled) Cumulative bytes received |
| `latency.avg_ms` | number | (When latency enabled) Average latency in milliseconds |
| `latency.min_ms` | number | (When latency enabled) Minimum observed latency |
| `latency.max_ms` | number | (When latency enabled) Maximum observed latency |

**Note:** Edges are ordered by public key (lower first). `a.bandwidth.sent` should approximately equal `b.bandwidth.recv`.

---

### GET /metrics/{ids}

Returns metrics for specific transport(s). Accepts comma-separated IDs. Includes data for expired transports.

**Request:**

```
GET /metrics/{id}
GET /metrics/{id1},{id2},{id3}
GET /metrics/{ids}?days=7
GET /metrics/{ids}?edges=true
GET /metrics/{ids}?bandwidth=false
GET /metrics/{ids}?latency=false
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `ids` | string | One or more transport UUIDs, comma-separated |

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `days` | integer | 0 | Number of days of history (0 = all available, max 35) |
| `edges` | boolean | false | Include actual public keys in response |
| `bandwidth` | boolean | true | Include bandwidth data in response |
| `latency` | boolean | true | Include latency data in response |

**Response:**

```json
[
    {
        "id": "<transport-id-1>",
        "type": "stcpr",
        "live": true,
        "daily": [
            {
                "date": "2024-02-21",
                "a": {
                    "bandwidth": {
                        "sent": 536870912,
                        "recv": 268435456
                    },
                    "latency": {
                        "avg_ms": 45.2,
                        "min_ms": 12.0,
                        "max_ms": 120.5
                    }
                },
                "b": {
                    "bandwidth": {
                        "sent": 268435456,
                        "recv": 536870912
                    },
                    "latency": {
                        "avg_ms": 44.8,
                        "min_ms": 11.5,
                        "max_ms": 118.0
                    }
                }
            }
        ]
    }
]
```

---

### GET /metric/visor/{pks}

Returns aggregate metrics for one or more visors. Accepts comma-separated public keys.

**Request:**

```
GET /metric/visor/{pk}
GET /metric/visor/{pk1},{pk2},{pk3}
GET /metric/visor/{pks}?days=7
GET /metric/visor/{pks}?bandwidth=false
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `pks` | string | One or more visor public keys, comma-separated |

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `days` | integer | 0 | Number of days of history (0 = all available, max 35) |
| `bandwidth` | boolean | true | Include bandwidth data in response |
| `latency` | boolean | true | Include latency data in response |

**Response (single PK):**

```json
{
    "<visor-pk>": {
        "daily": [
            {
                "date": "2024-02-21",
                "bandwidth": { "sent": 1073741824, "recv": 536870912, "total": 1610612736 },
                "latency": 42.5
            }
        ],
        "cumulative": {
            "bandwidth": { "sent": 37580963840, "recv": 18790481920, "total": 56371445760 },
            "latency": 41.2
        }
    }
}
```

**Response (multiple PKs):**

```json
{
    "<visor-pk-1>": {
        "daily": [...],
        "cumulative": { "bandwidth": {...}, "latency": 41.2 }
    },
    "<visor-pk-2>": {
        "daily": [...],
        "cumulative": { "bandwidth": {...}, "latency": 38.5 }
    }
}
```

---

### GET /metrics/visor/{pks}

Returns per-transport metrics for one or more visors. Accepts comma-separated public keys. Includes historical data for expired transports by default.

**Request:**

```
GET /metrics/visor/{pk}
GET /metrics/visor/{pk1},{pk2},{pk3}
GET /metrics/visor/{pks}?days=7
GET /metrics/visor/{pks}?type=stcpr
GET /metrics/visor/{pks}?live=true
GET /metrics/visor/{pks}?edges=true
GET /metrics/visor/{pks}?bandwidth=false
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `pks` | string | One or more visor public keys, comma-separated |

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `days` | integer | 0 | Number of days of history (0 = all available, max 35) |
| `type` | string | (all) | Filter by transport type: `stcpr`, `sudph`, `dmsg`, `stcp` |
| `live` | string | `all` | Filter by transport liveness: `true` (only live), `false` (only expired), `all` (both) |
| `edges` | boolean | false | Include actual public keys in response |
| `bandwidth` | boolean | true | Include bandwidth data in response |
| `latency` | boolean | true | Include latency data in response |

**Response:**

```json
[
    {
        "id": "<transport-id>",
        "type": "stcpr",
        "live": true,
        "daily": [
            {
                "date": "2024-02-21",
                "a": {
                    "bandwidth": { "sent": 536870912, "recv": 268435456 },
                    "latency": { "avg_ms": 45.2, "min_ms": 12.0, "max_ms": 120.5 }
                },
                "b": {
                    "bandwidth": { "sent": 268435456, "recv": 536870912 },
                    "latency": { "avg_ms": 44.8, "min_ms": 11.5, "max_ms": 118.0 }
                }
            }
        ]
    }
]
```

---

## Health Check

### GET Health

Returns the health status and build information of the Transport Discovery service.

**Request:**

```
GET /health
```

**Response:**

- 200 OK (Success).
    ```json
    {
        "build_info": {
            "version": "v1.3.34",
            "commit": "abc123def",
            "date": "2024-02-21T10:00:00Z"
        },
        "started_at": "2024-02-21T08:00:00Z",
        "dmsg_address": "<public-key>",
        "dmsg_servers": ["<server-pk-1>", "<server-pk-2>"]
    }
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `build_info` | object | Build information for the running service |
| `build_info.version` | string | Skywire version |
| `build_info.commit` | string | Git commit hash |
| `build_info.date` | string | Build timestamp |
| `started_at` | string | ISO 8601 timestamp when the service started |
| `dmsg_address` | string | DMSG public key of this TPD instance (if configured) |
| `dmsg_servers` | array | List of DMSG server public keys (if configured) |

---

## Endpoint Summary

### Core Endpoints

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/security/nonces/{pk}` | GET | No | Get next expected nonce |
| `/transports/id:{id}` | GET | Yes | Get transport by ID |
| `/transports/edge:{pk}` | GET | Yes | Get transports by edge |
| `/transports/stats/{pk}` | GET | No | Get transport stats for edge |
| `/transports/` | POST | Yes | Register transport(s) (supports `sync` param for TPD data sync) |
| `/transports/id:{id}` | DELETE | Yes | Delete a transport |
| `/transports/delete-batch` | POST | Yes | Delete multiple transports |
| `/all-transports` | GET | No | Get all transports |
| `/all-transports/stats` | GET | No | Get aggregate stats |
| `/all-transports/per-key-stats` | GET | No | Get per-visor stats |
| `/uptimes` | GET | No | Get visor uptimes |
| `/health` | GET | No | Health check |

### Metrics Endpoints

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/metric` | GET | No | Network-wide aggregate (supports `bandwidth`, `latency` params) |
| `/metric/visor/{pks}` | GET | No | Visor aggregate, comma-separated PKs (supports `bandwidth`, `latency` params) |
| `/metrics` | GET | No | All transports (supports `live`, `edges`, `bandwidth`, `latency` params) |
| `/metrics/{ids}` | GET | No | Specific transport(s), comma-separated IDs (supports `edges`, `bandwidth`, `latency` params) |
| `/metrics/visor/{pks}` | GET | No | All transports for visor(s), comma-separated PKs (supports `live`, `edges`, `bandwidth`, `latency` params) |
