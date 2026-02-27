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

**Submitting Transport Statuses:**

If a given *Transport* is public, the associated *Transport Edges* is responsible for submitting their individual *Transport Statuses* to the *Transport Discovery* whenever the follow events occur;

- Directly after a *Transport* is first successfully registered in the *Transport Discovery*.
- Whenever the *Transport* comes online/offline (connected/disconnected).

**Obtaining Transports:**

There are two ways to obtain transports; either via the assigned *Transport ID*, or via one of the *Transport Edges*. There is no restriction as who can access this information and results can be sorted by a given meta.

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

The following is a summary of all the *Transport Discovery* endpoints.

- `GET /security/nonces/edge:<public-key>`
- `GET /transports/id:<transport-id>`
- `GET /transports/edge:<public-key>`
- `POST /transports`
- `POST /statuses`

All endpoints should include an `Accept: application/json` field and the response header should include an `Content-Type: application/json` field.

All requests (except for obtaining the next expected incrementing nonce) should include the following fields.

```
Accept: application/json
Content-Type: application/json
SW-Public: <public-key>
SW-Nonce: <nonce>
SW-Sig: <signature>
```

### GET Incrementing Security Nonce

Obtains the next expected incrementing nonce for a given edge's public key.

**Request:**

```
GET /security/nonces/<public-key>
```

**Responses:**

- 200 OK (Success).
    ```json
    {
        "edge": "<public-key>",
        "next_nonce": 0
    }
    ```
- 400 Bad Request (Malformed request).
- 500 Internal Server Error (Server error).

### GET Transport Entry via Transport ID

Obtains a *Transport* via a given *Transport ID*.

Should only return a single `"transport"` result.

**Request:**

```
GET /transports/id:<transport-id>
```

**Responses:**

- 200 OK (Success).
    ```json
    {
        "entry": {
            "id": "<transport-id>",
            "edges": [
                "<public-key-1>",
                "<public-key-2>"
            ],
            "type": "<transport-type>"
        },
        "is_up": true,
        "registered": 0
    }
    ```
- 400 Bad Request (Malformed request).
- 500 Internal Server Error (Server error).

### GET Transport(s) via Edge Public Key

Obtains *Transport(s)* via a given *Transport Edge* public key.

**Request:**

```
GET /transports/edge:<public-key>
```

**Responses:**

- 200 OK (Success).
    ```json
    [
        {
            "entry": {
                "t_id": "<transport-id-1>",
                "edges": [
                    "<public-key-1>",
                    "<public-key-2>"
                ],
                "type": "<transport-type>",
                "public": true
            },
            "is_up": true,
            "registered": 0
        },
        {
            "entry": {
                "t_id": "<transport-id-2>",
                "edges": [
                    "<public-key-1>",
                    "<public-key-2>"
                ],
                "type": "<transport-type>",
                "public": true
            },
            "is_up": false,
            "registered": 0
        }
    ]
    ```
- 400 Bad Request (Malformed request).
- 500 Internal Server Error (Server error).

### POST Register Transport(s)

Registers one or multiple Transports.

**Request:**

```
POST /transports
```

```json
[
    {
        "entry": {
            "id": "<transport-id-1>",
            "edges": [
                "<public-key-1>",
                "<public-key-2>"
            ],
            "type": "<transport-type-1>",
            "public": true
        },
        "signatures": [
            "<signature-1>",
            "<signature-2>"
        ]
    },
    {
        "entry": {
            "id": "<transport-id-2>",
            "edges": [
                "<public-key-1>",
                "<public-key-3>"
            ],
            "type": "<transport-type-2>",
            "public": true
        },
        "signatures": [
            "<signature-1>",
            "<signature-3>"
        ]
    }
]    
```

**Responses:**

- 200 OK (Success).
    ```json
    [
        {
            "entry": {
                "id": "<transport-id-1>",
                "edges": [
                    "<public-key-1>",
                    "<public-key-2>"
                ],
                "type": "<transport-type-1>",
                "public": true
            },
            "signatures": [
                "<signature-1>",
                "<signature-2>"
            ],
            "registered": 0
        },
        {
            "entry": {
                "id": "<transport-id-2>",
                "edges": [
                    "<public-key-1>",
                    "<public-key-3>"
                ],
                "type": "<transport-type-2>",
                "public": true
            },
            "signatures": [
                "<signature-1>",
                "<signature-3>"
            ],
            "registered": 0
        }
    ]
    ```
- 400 Bad Request (Malformed request).
- 401 Unauthorized (Invalid signature/nonce).
- 408 Request Timeout (Timed out).
- 500 Internal Server Error (Server error).

### POST Status(es)

Submits one or multiple *Transport Status(es)* from the perspective of the submitting node. The returned result is the final *Transport Status(es)* determined by the *Transport Discovery* that is generated using the submitted *Transport Status(es)* of the two edges.

When a Transport is registered, it is considered to be *up*. Then after, every time a node's *Status* is submitted, the *Transport Discovery* alters the final state *Status* with the following rules:

- If there is only one edge's *Status* submitted, the final status is of that of the submitted *Status*.
- If there are two *Status*es submitted and they both agree, final *Status* will also be the same.
- If the two submitted *Status*es disagree, then the final *Status* is always *Down*.

**Request:**

```
POST /statuses
```

```json
[
    {
        "id": "<transport-id-1>",
        "is_up": true
    },
    {
        "id": "<transport-id-2>",
        "is_up": true
    }
]
```

**Responses:**

- 200 OK (Success).
    ```json
    [
        {
            "id": "<transport-id-1>",
            "is_up": true,
            "updated": 0
        },
        {
            "id": "<transport-id-2>",
            "is_up": false,
            "updated": 0
        }
    ]
    ```
- 400 Bad Request (Malformed request).
- 401 Unauthorized (Invalid signature/nonce).
- 408 Request Timeout (Timed out).
- 500 Internal Server Error (Server error).

---

## Extended Endpoints

The following endpoints extend the core Transport Discovery functionality with network visibility, uptime tracking, and quality-of-service metrics.

### GET All Transports

Returns all registered public transports. This endpoint provides the core transport registry data only, without QoS metrics.

**Request:**

```
GET /all-transports
GET /all-transports?selfTransports=hide
```

**Query Parameters:**

- `selfTransports` (optional): Set to `hide` to exclude transports where both edges are the same visor.

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

### GET All Transports Stats

Returns aggregate statistics about all transports.

**Request:**

```
GET /all-transports/stats
```

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

### GET Per-Key Stats

Returns transport counts per visor public key.

**Request:**

```
GET /all-transports/per-key-stats
```

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

## Quality of Service (QoS) Metrics

QoS metrics (bandwidth and latency) are served from dedicated endpoints, separate from core transport data. This separation ensures that the core transport registry remains lightweight and that QoS data can be queried independently.

### Reporting Configuration

| Parameter | Default Value | Description |
|-----------|---------------|-------------|
| Transport re-registration interval | 90 seconds | How often visors re-register their transports |
| Transport entry TTL | 2 minutes | Time before an unrefreshed transport is considered stale |
| Uptime cache refresh | 5 minutes | How often uptime data is refreshed from cache |
| Daily bandwidth retention | 35 days | How long daily bandwidth aggregates are kept |
| Visor tracking retention | 400 days | How long visor records are kept |

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
                "sent": 536870912,
                "recv": 268435456
            },
            "edge_b": {
                "pk": "<public-key-2>",
                "sent": 268435456,
                "recv": 536870912
            }
        },
        {
            "date": "2024-02-20",
            "edge_a": {
                "pk": "<public-key-1>",
                "sent": 400000000,
                "recv": 200000000
            },
            "edge_b": {
                "pk": "<public-key-2>",
                "sent": 200000000,
                "recv": 400000000
            }
        }
    ]
    ```

**Note:** Edge A's `sent` should approximately equal Edge B's `recv` and vice versa. Significant discrepancies may indicate measurement issues or network problems.

#### GET Visor Bandwidth

Returns aggregated bandwidth for all transports belonging to a visor.

**Request:**

```
GET /bandwidth/visor/{pk}
GET /bandwidth/visor/{pk}?period=daily&limit=7
```

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

### Transport Metrics

The `/metrics` endpoint provides detailed QoS metrics (bandwidth and latency) for all transports, organized by reporting visor.

#### GET All Transport Metrics

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

#### GET Visor Transport Metrics

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

#### POST Report Metrics

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

#### GET Verified Metrics

Returns verified QoS metrics for transports where both edges have reported consistent bandwidth data. This endpoint only includes transports where both edges' reports agree within an acceptable margin (e.g., 5% discrepancy).

**Request:**

```
GET /metrics/verified
GET /metrics/verified?discrepancy_threshold=0.05
```

**Query Parameters:**

- `discrepancy_threshold` (optional): Maximum allowed discrepancy between edges (0.0-1.0). Default: 0.05 (5%).

**Response:**

- 200 OK (Success).
    ```json
    [
        {
            "transport_id": "<transport-id>",
            "edges": ["<public-key-1>", "<public-key-2>"],
            "bandwidth": {
                "sent": 536870912,
                "recv": 268435456,
                "total": 805306368
            },
            "latency_ms": 45.0,
            "verified_at": "2024-02-21T15:30:00Z"
        }
    ]
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `transport_id` | string | Transport UUID |
| `edges` | array | Public keys of both transport edges |
| `bandwidth.sent` | integer | Verified bytes sent (average of both edges' reports) |
| `bandwidth.recv` | integer | Verified bytes received |
| `bandwidth.total` | integer | Total verified bandwidth |
| `latency_ms` | number | Average latency from both edges |
| `verified_at` | string | Timestamp when metrics were last verified |

#### GET Transport Latency

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

## Endpoint Summary

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/security/nonces/{pk}` | GET | No | Get next expected nonce |
| `/transports/id:{id}` | GET | Yes | Get transport by ID |
| `/transports/edge:{pk}` | GET | Yes | Get transports by edge |
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
| `/latency/transport/{id}` | GET | No | Get transport latency history (both edges) |
| `/metrics` | GET | No | Get all QoS metrics by visor |
| `/metrics` | POST | Yes | Report QoS metrics |
| `/metrics/visor/{pk}` | GET | No | Get visor QoS metrics |
| `/metrics/verified` | GET | No | Get verified metrics (consistent edge reports) |
| `/health` | GET | No | Health check |
