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
| `stcpr` | STCP Relay | TCP connection via a relay server. Most reliable but adds latency. |
| `sudph` | SUDP Hole-punch | UDP hole-punching for direct peer-to-peer connections. Lower latency but requires NAT traversal. |
| `stcp` | STCP | Direct TCP connection (typically used in local networks). |
| `dmsg` | DMSG | Connection over the DMSG network overlay. |

**Transport Type Selection:**

- `stcpr` is used when direct connections are not possible (e.g., behind restrictive NATs)
- `sudph` is preferred for direct connections when UDP hole-punching succeeds
- `dmsg` provides an alternative routing path through the DMSG network

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
- `GET /bandwidth/transport/{id}` - Get transport bandwidth history
- `GET /bandwidth/visor/{pk}` - Get visor bandwidth history
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
| `type` | string | Transport type: `stcpr` (STCP Relay), `sudph` (SUDP Hole-punch), or `dmsg` |
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
```

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

**Response:**

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

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `entry` | object | The registered transport entry |
| `signatures` | array | The signatures provided during registration |
| `registered` | integer | Unix timestamp when the transport was first registered |

**Error Responses:**

- 400 Bad Request (Malformed request, invalid entry format).
- 401 Unauthorized (Invalid signature or nonce).
- 408 Request Timeout (Request took too long).
- 409 Conflict (Transport with same edges already exists).
- 500 Internal Server Error (Server error).

### POST Status(es) *(Deprecated)*

> **⚠️ DEPRECATED:** This endpoint returns `410 Gone`. Transport status is now determined automatically by the re-registration mechanism. Transports that are not re-registered within the 2-minute TTL are considered down and expire from the registry. See "Transport Status via Re-registration" in the procedures section above.

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
| `by_type.stcpr` | integer | Number of STCP Relay transports |
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
| `stcpr` | integer | Number of STCP Relay transports (if any) |
| `sudph` | integer | Number of SUDP Hole-punch transports (if any) |
| `dmsg` | integer | Number of DMSG transports (if any) |

**Note:** Only transport types that have a count > 0 are included in the response for each visor.

**Error Responses:**

- 500 Internal Server Error (Server error).

---

## Quality of Service (QoS) Metrics

QoS metrics (bandwidth and latency) are served from dedicated endpoints, separate from core transport data. This separation ensures that the core transport registry remains lightweight and that QoS data can be queried independently.

### Reporting Configuration

| Parameter | Default Value | Description |
|-----------|---------------|-------------|
| Transport re-registration interval | 90 seconds | How often visors re-register their transports |
| Transport entry TTL | 2 minutes | Time before an unrefreshed transport is considered stale |
| Uptime cache refresh | 5 minutes | How often the uptime data cache is refreshed |
| Daily bandwidth retention | 35 days | How long daily bandwidth aggregates are kept in Redis |
| Verified metrics retention | 7 days | How long daily verified metrics (bandwidth + latency) are kept |
| Visor PK set TTL | 400 days | TTL for the set of known visor public keys (for `/visors` endpoint) |

**Note on Data Retention:**
- The Uptime Tracker keeps data in the database for **7 days** (configurable via `--store-data-cutoff`)
- Older uptime data is archived to JSON files (`{date}-uptime-data.json`)
- The `/uptimes` endpoint returns the last 7 days of daily uptime percentages
- Verified metrics (`/metrics/verified`) are retained for **7 days** to match uptime data retention

### Bandwidth Semantics

Bandwidth values are **cumulative** - they represent total bytes sent/received since the transport was established, not per-interval deltas. Each transport edge reports its own perspective:

```go
type BandwidthData struct {
    SentBytes uint64 // Total bytes sent (cumulative)
    RecvBytes uint64 // Total bytes received (cumulative)
}
```

### Bandwidth Metrics

Bandwidth is measured in bytes and reported by each transport edge. The Transport Discovery stores bandwidth data from both edges independently.

#### GET Transport Bandwidth

Returns historical bandwidth for a specific transport, showing reports from both edges.

**Request:**

```
GET /bandwidth/transport/{id}
GET /bandwidth/transport/{id}?period=daily&limit=7
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | string | Transport UUID |

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `period` | string | `daily` | Aggregation period: `daily` or `hourly` |
| `limit` | integer | 7 | Number of periods to return |

**Response:**

- 200 OK (Success).
    ```json
    [
        {
            "date": "2024-02-21",
            "edge_a": {
                "pk": "<public-key-1>",
                "sent": 536870912,
                "recv": 268435456
            },
            "edge_b": {
                "pk": "<public-key-2>",
                "sent": 268435456,
                "recv": 536870912
            }
        }
    ]
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `date` | string | Date/time for this period (YYYY-MM-DD for daily, ISO 8601 for hourly) |
| `edge_a` | object | Bandwidth reported by the first edge (sorted by PK) |
| `edge_a.pk` | string | Public key of this edge |
| `edge_a.sent` | integer | Cumulative bytes sent as reported by this edge |
| `edge_a.recv` | integer | Cumulative bytes received as reported by this edge |
| `edge_b` | object | Bandwidth reported by the second edge |
| `edge_b.pk` | string | Public key of this edge |
| `edge_b.sent` | integer | Cumulative bytes sent as reported by this edge |
| `edge_b.recv` | integer | Cumulative bytes received as reported by this edge |

**Note:** Edge A's `sent` should approximately equal Edge B's `recv` and vice versa. Significant discrepancies may indicate measurement issues or network problems.

**Error Responses:**

- 400 Bad Request (Invalid transport ID format).
- 404 Not Found (Transport not found or no bandwidth data).
- 500 Internal Server Error (Server error).

#### GET Visor Bandwidth

Returns aggregated bandwidth for all transports belonging to a visor.

**Request:**

```
GET /bandwidth/visor/{pk}
GET /bandwidth/visor/{pk}?period=daily&limit=7
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `pk` | string | Visor public key (hex-encoded) |

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `period` | string | `daily` | Aggregation period: `daily` or `hourly` |
| `limit` | integer | 7 | Number of periods to return |

**Response:**

- 200 OK (Success).
    ```json
    [
        {
            "date": "2024-02-21",
            "bandwidth": 5368709120
        }
    ]
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `date` | string | Date/time for this period (YYYY-MM-DD for daily) |
| `bandwidth` | integer | Total bytes (sent + received) across all transports for this visor |

**Error Responses:**

- 400 Bad Request (Invalid public key format).
- 404 Not Found (Visor not found or no bandwidth data).
- 500 Internal Server Error (Server error).

### Transport Metrics *(Proposed)*

> **Note:** The `/metrics` endpoints described in this section are **proposed** and not yet implemented. Currently, only the `/bandwidth/*` endpoints above are available for QoS data.

The `/metrics` endpoint will provide detailed QoS metrics (bandwidth and latency) for all transports, organized by reporting visor.

#### GET All Transport Metrics *(Proposed)*

Returns QoS metrics for all transports, organized by the reporting visor's public key. Each visor reports metrics for its own perspective of each transport.

**Request:**

```
GET /metrics
```

**Response:**

- 200 OK (Success).
    ```json
    {
        "<visor-pk-1>": {
            "transports": {
                "<transport-id-1>": {
                    "bandwidth": {
                        "sent": 536870912,
                        "recv": 268435456,
                        "total": 805306368
                    },
                    "latency": {
                        "avg_ms": 45.2,
                        "min_ms": 12.0,
                        "max_ms": 120.5
                    },
                    "updated": "2024-02-21T15:30:00Z"
                },
                "<transport-id-2>": {
                    "bandwidth": {
                        "sent": 134217728,
                        "recv": 67108864,
                        "total": 201326592
                    },
                    "latency": {
                        "avg_ms": 28.7,
                        "min_ms": 8.0,
                        "max_ms": 85.0
                    },
                    "updated": "2024-02-21T15:28:00Z"
                }
            }
        },
        "<visor-pk-2>": {
            "transports": {
                "<transport-id-1>": {
                    "bandwidth": {
                        "sent": 268435456,
                        "recv": 536870912,
                        "total": 805306368
                    },
                    "latency": {
                        "avg_ms": 44.8,
                        "min_ms": 11.5,
                        "max_ms": 118.0
                    },
                    "updated": "2024-02-21T15:30:00Z"
                }
            }
        }
    }
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `<visor-pk>` | object | Metrics reported by this visor |
| `transports` | object | Map of transport ID to metrics |
| `bandwidth.sent` | integer | Bytes sent over this transport |
| `bandwidth.recv` | integer | Bytes received over this transport |
| `bandwidth.total` | integer | Total bytes (sent + recv) |
| `latency.avg_ms` | number | Average round-trip latency in milliseconds |
| `latency.min_ms` | number | Minimum observed latency |
| `latency.max_ms` | number | Maximum observed latency |
| `updated` | string | ISO 8601 timestamp of last metric update |

#### GET Visor Transport Metrics *(Proposed)*

Returns QoS metrics for all transports belonging to a specific visor.

**Request:**

```
GET /metrics/visor/{pk}
```

**Response:**

- 200 OK (Success).
    ```json
    {
        "pk": "<visor-pk>",
        "transports": {
            "<transport-id-1>": {
                "bandwidth": {
                    "sent": 536870912,
                    "recv": 268435456,
                    "total": 805306368
                },
                "latency": {
                    "avg_ms": 45.2,
                    "min_ms": 12.0,
                    "max_ms": 120.5
                },
                "updated": "2024-02-21T15:30:00Z"
            }
        }
    }
    ```

#### POST Report Metrics *(Proposed)*

Allows a visor to report QoS metrics for its transports. This endpoint is authenticated.

**Request:**

```
POST /metrics
```

**Headers:**

```
SW-Public: <public-key>
SW-Nonce: <nonce>
SW-Sig: <signature>
```

**Body:**

```json
{
    "transports": {
        "<transport-id-1>": {
            "bandwidth": {
                "sent": 536870912,
                "recv": 268435456
            },
            "latency": {
                "avg_ms": 45.2,
                "samples": 100
            }
        }
    }
}
```

**Responses:**

- 200 OK (Success).
- 400 Bad Request (Malformed request).
- 401 Unauthorized (Invalid signature/nonce).
- 500 Internal Server Error (Server error).

#### GET Verified Metrics *(Proposed)*

Returns daily verified QoS metrics for transports where both edges have reported consistent bandwidth data. This endpoint only includes transports where both edges' reports agree within an acceptable margin (e.g., 5% discrepancy). History is retained for 7 days (same as uptime data).

**Request:**

```
GET /metrics/verified
GET /metrics/verified?limit=7&discrepancy_threshold=0.05
```

**Query Parameters:**

- `limit` (optional): Number of days of history to return. Default: 7.
- `discrepancy_threshold` (optional): Maximum allowed discrepancy between edges (0.0-1.0). Default: 0.05 (5%).

**Response:**

- 200 OK (Success).
    ```json
    [
        {
            "date": "2024-02-21",
            "transports": [
                {
                    "transport_id": "<transport-id>",
                    "edges": ["<public-key-1>", "<public-key-2>"],
                    "bandwidth": {
                        "sent": 536870912,
                        "recv": 268435456,
                        "total": 805306368
                    },
                    "latency": {
                        "avg_ms": 45.0,
                        "min_ms": 12.0,
                        "max_ms": 120.5
                    }
                }
            ]
        },
        {
            "date": "2024-02-20",
            "transports": [
                {
                    "transport_id": "<transport-id>",
                    "edges": ["<public-key-1>", "<public-key-2>"],
                    "bandwidth": {
                        "sent": 400000000,
                        "recv": 200000000,
                        "total": 600000000
                    },
                    "latency": {
                        "avg_ms": 42.3,
                        "min_ms": 10.5,
                        "max_ms": 115.0
                    }
                }
            ]
        }
    ]
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `date` | string | Date for this daily aggregate (YYYY-MM-DD) |
| `transports` | array | Transports with verified metrics for this day |
| `transport_id` | string | Transport UUID |
| `edges` | array | Public keys of both transport edges |
| `bandwidth.sent` | integer | Verified bytes sent (average of both edges' reports) |
| `bandwidth.recv` | integer | Verified bytes received |
| `bandwidth.total` | integer | Total verified bandwidth |
| `latency.avg_ms` | number | Average latency (average of both edges) |
| `latency.min_ms` | number | Minimum observed latency for the day |
| `latency.max_ms` | number | Maximum observed latency for the day |

#### GET Transport Latency *(Proposed)*

Returns latency history for a specific transport, showing measurements from both edges.

**Request:**

```
GET /latency/transport/{id}
GET /latency/transport/{id}?period=daily&limit=7
```

**Query Parameters:**

- `period` (optional): Aggregation period. Values: `daily` (default), `hourly`.
- `limit` (optional): Number of periods to return. Default: 7.

**Response:**

- 200 OK (Success).
    ```json
    [
        {
            "date": "2024-02-21",
            "edge_a": {
                "pk": "<public-key-1>",
                "avg_ms": 45.2,
                "min_ms": 12.0,
                "max_ms": 120.5
            },
            "edge_b": {
                "pk": "<public-key-2>",
                "avg_ms": 44.8,
                "min_ms": 11.5,
                "max_ms": 118.0
            }
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
        "dmsg_address": "dmsg://...",
        "dmsg_servers": ["dmsg://server1...", "dmsg://server2..."]
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
| `dmsg_address` | string | DMSG address of this TPD instance (if configured) |
| `dmsg_servers` | array | List of DMSG server addresses (if configured) |

---

## Endpoint Summary

### Implemented Endpoints

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/security/nonces/{pk}` | GET | No | Get next expected nonce |
| `/transports/id:{id}` | GET | Yes | Get transport by ID |
| `/transports/edge:{pk}` | GET | Yes | Get transports by edge |
| `/transports/stats/{pk}` | GET | No | Get transport stats for edge |
| `/transports/` | POST | Yes | Register transport(s) |
| `/transports/id:{id}` | DELETE | Yes | Delete a transport |
| `/transports/delete-batch` | POST | Yes | Delete multiple transports |
| `/statuses` | POST | Yes | **Deprecated** - Returns 410 Gone |
| `/all-transports` | GET | No | Get all transports (no QoS) |
| `/all-transports/stats` | GET | No | Get aggregate stats |
| `/all-transports/per-key-stats` | GET | No | Get per-visor stats |
| `/uptimes` | GET | No | Get visor uptimes (no QoS) |
| `/bandwidth/transport/{id}` | GET | No | Get transport bandwidth history (both edges) |
| `/bandwidth/visor/{pk}` | GET | No | Get visor bandwidth history |
| `/health` | GET | No | Health check |

### Proposed Endpoints (Not Yet Implemented)

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/metrics` | GET | No | Get all QoS metrics by visor |
| `/metrics` | POST | Yes | Report QoS metrics |
| `/metrics/visor/{pk}` | GET | No | Get visor QoS metrics |
| `/metrics/verified` | GET | No | Get verified metrics (consistent edge reports) |
| `/latency/transport/{id}` | GET | No | Get transport latency history (both edges) |
