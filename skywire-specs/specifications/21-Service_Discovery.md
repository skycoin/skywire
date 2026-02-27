# Service Discovery

The *Service Discovery* (SD) is a registry service that allows Skywire applications and services to advertise their availability and for clients to discover them.

## Overview

The Service Discovery maintains a registry of:

- **VPN servers**: Public VPN exit nodes
- **SOCKS5 proxies**: Proxy services running on visors
- **Skysocks servers**: Skywire-native proxy servers
- **Custom services**: Other application-defined services

Each service entry includes:

- The visor's public key hosting the service
- Service type and port
- Geographic location (country, region)
- Service-specific metadata

## Code Structure

The code should be in the `skycoin/skywire-services` repository:

- `/cmd/service-discovery/service-discovery.go` is the main executable for the *Service Discovery*.
- `/pkg/service-discovery/api/` contains the RESTFUL API definitions.
- `/pkg/service-discovery/store/` contains the database storage logic.

## Authentication

Service registration and removal operations are authenticated using signature-based authentication:

**Request Headers:**

| Header | Description |
|--------|-------------|
| `SW-Public` | Visor's public key (hex encoded) |
| `SW-Nonce` | Incrementing nonce for replay protection |
| `SW-Sig` | Ed25519 signature of the request |

Read-only operations (listing services) do not require authentication.

## Database

The Service Discovery uses PostgreSQL to store:

- Service entries with metadata
- Geographic information (derived from IP or provided)
- Registration timestamps and TTL

## Endpoint Definitions

All endpoint calls should include an `Accept: application/json` field in the request header, and the response header should include a `Content-Type: application/json` field.

### GET Services List

Returns a list of registered services, optionally filtered.

**Request:**

```
GET /api/services
```

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `type` | string | No | Service type filter (e.g., "vpn", "skysocks") |
| `country` | string | No | Country code filter (ISO 3166-1 alpha-2) |
| `version` | string | No | Minimum version filter |
| `limit` | integer | No | Maximum results (default: 100) |
| `offset` | integer | No | Pagination offset (default: 0) |

**Response:**

- 200 OK (Success).
    ```json
    [
        {
            "address": "02abc123...:3000",
            "type": "vpn",
            "geo": {
                "country": "US",
                "region": "California",
                "city": "Los Angeles",
                "lat": 34.0522,
                "lon": -118.2437
            },
            "version": "v2.0.0",
            "registered_at": "2024-02-25T10:30:00Z"
        }
    ]
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `address` | string | Service address (pk:port format) |
| `type` | string | Service type identifier |
| `geo` | object | Geographic location information |
| `geo.country` | string | ISO 3166-1 alpha-2 country code |
| `geo.region` | string | State/province/region name |
| `geo.city` | string | City name |
| `geo.lat` | number | Latitude |
| `geo.lon` | number | Longitude |
| `version` | string | Service/visor version |
| `registered_at` | string | Registration timestamp (RFC3339) |

### POST Register Service

Registers a new service or updates an existing registration.

**Request:**

```
POST /api/services
Content-Type: application/json
SW-Public: <visor-public-key>
SW-Nonce: <incrementing-nonce>
SW-Sig: <signature>
```

```json
{
    "address": "02abc123...:3000",
    "type": "vpn",
    "geo": {
        "country": "US",
        "region": "California"
    },
    "version": "v2.0.0"
}
```

**Request Fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `address` | string | Yes | Service address (pk:port format) |
| `type` | string | Yes | Service type identifier |
| `geo` | object | No | Geographic location (auto-detected if not provided) |
| `version` | string | No | Service/visor version |

**Responses:**

- 200 OK (Success - updated existing).
    ```json
    {
        "status": "updated",
        "address": "02abc123...:3000"
    }
    ```

- 201 Created (Success - new registration).
    ```json
    {
        "status": "created",
        "address": "02abc123...:3000"
    }
    ```

- 400 Bad Request (Invalid address or missing fields).
    ```json
    {
        "error": {
            "code": 400,
            "message": "invalid address format"
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

### DELETE Deregister Service

Removes a service registration.

**Request:**

```
DELETE /api/services/{addr}
SW-Public: <visor-public-key>
SW-Nonce: <incrementing-nonce>
SW-Sig: <signature>
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `addr` | string | Service address to deregister (URL-encoded pk:port) |

**Responses:**

- 200 OK (Success).
    ```json
    {
        "status": "ok"
    }
    ```

- 401 Unauthorized (Invalid signature or not the owner).
    ```json
    {
        "error": {
            "code": 401,
            "message": "unauthorized"
        }
    }
    ```

- 404 Not Found (Service not registered).
    ```json
    {
        "error": {
            "code": 404,
            "message": "service not found"
        }
    }
    ```

### GET Service Types

Returns a list of available service types.

**Request:**

```
GET /api/services/types
```

**Response:**

- 200 OK (Success).
    ```json
    {
        "types": [
            {
                "name": "vpn",
                "description": "VPN exit node",
                "count": 150
            },
            {
                "name": "skysocks",
                "description": "Skywire SOCKS5 proxy",
                "count": 75
            }
        ]
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
        "stats": {
            "total_services": 225,
            "by_type": {
                "vpn": 150,
                "skysocks": 75
            }
        }
    }
    ```

---

## Service Types

| Type | Description |
|------|-------------|
| `vpn` | VPN exit node service |
| `skysocks` | Skywire SOCKS5 proxy |
| `skysocks-client` | Skysocks client (for reverse proxies) |
| `visor` | Generic visor service |

## Endpoint Summary

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/services` | GET | No | List registered services |
| `/api/services` | POST | SW-* headers | Register a service |
| `/api/services/{addr}` | DELETE | SW-* headers | Deregister a service |
| `/api/services/types` | GET | No | List service types |
| `/health` | GET | No | Health check |
