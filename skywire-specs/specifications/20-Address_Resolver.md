# Address Resolver

The *Address Resolver* (AR) is a service that maps Skywire Visor public keys to their network addresses. It enables visors to discover each other's reachable addresses for direct transport establishment.

## Overview

The Address Resolver maintains a registry of:

- **STCPR bindings**: Public key to IP:port mappings for STCPR (Skywire TCP Relay) transports
- **SUDPH bindings**: Public key to address mappings for SUDPH (Skywire UDP Hole Punching) transports

Visors register their addresses with the Address Resolver when they come online, and query it when they need to establish transports to other visors.

## Code Structure

The code should be in the `skycoin/skywire-services` repository:

- `/cmd/address-resolver/address-resolver.go` is the main executable for the *Address Resolver*.
- `/pkg/address-resolver/api/` contains the RESTFUL API definitions.
- `/pkg/address-resolver/store/` contains the database storage logic.

## Authentication

Visor requests that modify data (bind/unbind operations) are authenticated using signature-based authentication:

**Request Headers:**

| Header | Description |
|--------|-------------|
| `SW-Public` | Visor's public key (hex encoded) |
| `SW-Nonce` | Incrementing nonce for replay protection |
| `SW-Sig` | Ed25519 signature of the request |

Read-only operations (resolve) do not require authentication.

## Database

The Address Resolver uses Redis for fast key-value lookups:

- STCPR bindings: `stcpr:<pk>` → `<ip:port>`
- SUDPH bindings: `sudph:<pk>` → `<address>`
- Nonce tracking: `nonce:<pk>` → `<next_nonce>`

## Endpoint Definitions

All endpoint calls should include an `Accept: application/json` field in the request header, and the response header should include a `Content-Type: application/json` field.

### POST Bind STCPR

Registers an STCPR address binding for a visor.

**Request:**

```
POST /bind/stcpr
Content-Type: application/json
SW-Public: <visor-public-key>
SW-Nonce: <incrementing-nonce>
SW-Sig: <signature>
```

```json
{
    "port": 30001
}
```

**Request Fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `port` | integer | Yes | Port number for STCPR connections |

**Notes:**
- The IP address is automatically detected from the request's remote address
- The visor's public key is taken from the `SW-Public` header

**Responses:**

- 200 OK (Success).
    ```json
    {
        "status": "ok",
        "address": "192.168.1.1:30001"
    }
    ```

- 400 Bad Request (Invalid port or missing fields).
    ```json
    {
        "error": {
            "code": 400,
            "message": "invalid port number"
        }
    }
    ```

- 401 Unauthorized (Invalid signature).
    ```json
    {
        "error": {
            "code": 401,
            "message": "unauthorized"
        }
    }
    ```

### DELETE Unbind STCPR

Removes an STCPR address binding for a visor.

**Request:**

```
DELETE /bind/stcpr
SW-Public: <visor-public-key>
SW-Nonce: <incrementing-nonce>
SW-Sig: <signature>
```

**Responses:**

- 200 OK (Success).
    ```json
    {
        "status": "ok"
    }
    ```

- 401 Unauthorized (Invalid signature).
    ```json
    {
        "error": {
            "code": 401,
            "message": "unauthorized"
        }
    }
    ```

- 404 Not Found (No binding exists).
    ```json
    {
        "error": {
            "code": 404,
            "message": "binding not found"
        }
    }
    ```

### POST Bind SUDPH

Registers a SUDPH address binding for a visor.

**Request:**

```
POST /bind/sudph
Content-Type: application/json
SW-Public: <visor-public-key>
SW-Nonce: <incrementing-nonce>
SW-Sig: <signature>
```

```json
{
    "address": "192.168.1.1:40001"
}
```

**Request Fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `address` | string | Yes | Full address for SUDPH connections |

**Responses:**

- 200 OK (Success).
    ```json
    {
        "status": "ok"
    }
    ```

- 401 Unauthorized (Invalid signature).
    ```json
    {
        "error": {
            "code": 401,
            "message": "unauthorized"
        }
    }
    ```

### GET Resolve Address

Resolves a visor's network address by transport type.

**Request:**

```
GET /resolve/{type}/{pk}
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `type` | string | Transport type: "stcpr" or "sudph" |
| `pk` | string | Visor public key (hex) |

**Responses:**

- 200 OK (Success).
    ```json
    {
        "pk": "02abc123...",
        "type": "stcpr",
        "address": "192.168.1.1:30001",
        "registered_at": "2024-02-25T10:30:00Z"
    }
    ```

- 404 Not Found (No binding exists).
    ```json
    {
        "error": {
            "code": 404,
            "message": "address not found"
        }
    }
    ```

### GET All Transports

Returns all registered address bindings (for debugging/monitoring).

**Request:**

```
GET /transports
```

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `type` | string | No | Filter by transport type: "stcpr", "sudph" |
| `limit` | integer | No | Maximum results (default: 100) |
| `offset` | integer | No | Pagination offset (default: 0) |

**Response:**

- 200 OK (Success).
    ```json
    {
        "stcpr": [
            {
                "pk": "02abc123...",
                "address": "192.168.1.1:30001"
            }
        ],
        "sudph": [
            {
                "pk": "02def456...",
                "address": "192.168.1.2:40001"
            }
        ]
    }
    ```

### DELETE Deregister Network

Removes all bindings for a specific network type.

**Request:**

```
DELETE /deregister/{network}
SW-Public: <visor-public-key>
SW-Nonce: <incrementing-nonce>
SW-Sig: <signature>
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `network` | string | Network type to deregister: "stcpr", "sudph", or "all" |

**Responses:**

- 200 OK (Success).
    ```json
    {
        "status": "ok",
        "deregistered": ["stcpr"]
    }
    ```

- 401 Unauthorized (Invalid signature).
    ```json
    {
        "error": {
            "code": 401,
            "message": "unauthorized"
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
        "started_at": "2024-02-25T10:00:00Z"
    }
    ```

---

## Endpoint Summary

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/bind/stcpr` | POST | SW-* headers | Register STCPR binding |
| `/bind/stcpr` | DELETE | SW-* headers | Remove STCPR binding |
| `/bind/sudph` | POST | SW-* headers | Register SUDPH binding |
| `/resolve/{type}/{pk}` | GET | No | Resolve visor address |
| `/transports` | GET | No | List all bindings |
| `/deregister/{network}` | DELETE | SW-* headers | Remove network bindings |
| `/health` | GET | No | Health check |
