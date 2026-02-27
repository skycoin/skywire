# Uptime Tracker

The *Uptime Tracker* (UT) is a centralized service that monitors the online status and uptime of Skywire Visors. It is used to determine node eligibility for rewards and provides network health visibility.

## Overview

Visors periodically send heartbeat updates to the Uptime Tracker to indicate they are online and operational. The service tracks:

- **Online status**: Whether a visor is currently reachable
- **Uptime percentage**: The percentage of time a visor has been online over a period
- **Version information**: The skywire version each visor is running
- **IP address**: The public IP address of each visor

## Code Structure

The code should be in the `skycoin/skywire-services` repository:

- `/cmd/uptime-tracker/uptime-tracker.go` is the main executable for the *Uptime Tracker*.
- `/pkg/uptime-tracker/api/` contains the RESTFUL API definitions.
- `/pkg/uptime-tracker/store/` contains the database storage logic.

## Authentication

### Visor Authentication (for updates)

Visor requests are authenticated using signature-based authentication:

**Request Headers:**

| Header | Description |
|--------|-------------|
| `SW-Public` | Visor's public key (hex encoded) |
| `SW-Nonce` | Incrementing nonce for replay protection |
| `SW-Sig` | Ed25519 signature of the request |

### Network Monitor Authentication

Network Monitor requests use a separate authentication scheme:

**Request Headers:**

| Header | Description |
|--------|-------------|
| `NM-PK` | Network Monitor's public key |
| `NM-Sign` | Signature of the request body |

## Database

The Uptime Tracker uses PostgreSQL to store:

- Visor public keys and their online/offline status
- Uptime records with timestamps
- Version information history
- IP address records

## Endpoint Definitions

All endpoint calls should include an `Accept: application/json` field in the request header, and the response header should include a `Content-Type: application/json` field.

### GET Update (Heartbeat)

Registers a visor heartbeat, updating its online status.

**Request:**

```
GET /v4/update
SW-Public: <visor-public-key>
SW-Nonce: <incrementing-nonce>
SW-Sig: <signature>
```

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `version` | string | No | Skywire version (e.g., "v2.0.0") |
| `ip` | string | No | Visor's public IP address |

**Responses:**

- 200 OK (Success).
    ```json
    {
        "status": "ok"
    }
    ```

- 401 Unauthorized (Invalid signature or missing auth headers).
    ```json
    {
        "error": {
            "code": 401,
            "message": "unauthorized"
        }
    }
    ```

- 429 Too Many Requests (Rate limited).
    ```json
    {
        "error": {
            "code": 429,
            "message": "rate limit exceeded"
        }
    }
    ```

### GET Visors List

Returns a list of all registered visors with their current status.

**Request:**

```
GET /visors
```

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `status` | string | No | Filter by status: "online", "offline", or "all" (default: "all") |
| `version` | string | No | Filter by version prefix (e.g., "v2") |
| `limit` | integer | No | Maximum number of results (default: 100) |
| `offset` | integer | No | Pagination offset (default: 0) |

**Response:**

- 200 OK (Success).
    ```json
    [
        {
            "pk": "02abc123...",
            "online": true,
            "version": "v2.0.0",
            "ip": "192.168.1.1",
            "last_seen": "2024-02-25T10:30:00Z"
        }
    ]
    ```

### GET Uptimes

Returns uptime data for all visors. This endpoint is used by the Transport Discovery for uptime integration.

**Request:**

```
GET /uptimes
```

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `v` | string | No | API version ("v2" for extended format) |

**Response (v2 format):**

- 200 OK (Success).
    ```json
    [
        {
            "pk": "02abc123...",
            "online": true,
            "version": "v2.0.0"
        }
    ]
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `pk` | string | Visor public key (hex) |
| `online` | boolean | Current online status |
| `version` | string | Skywire version |

### GET Uptime by Public Key

Returns uptime information for a specific visor.

**Request:**

```
GET /uptime/{pk}
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `pk` | string | Visor public key (hex) |

**Response:**

- 200 OK (Success).
    ```json
    {
        "pk": "02abc123...",
        "online": true,
        "uptime": 99.5,
        "downtime": 0.5,
        "percentage": 99.5,
        "last_seen": "2024-02-25T10:30:00Z",
        "version": "v2.0.0",
        "ip": "192.168.1.1"
    }
    ```

- 404 Not Found (Visor not registered).
    ```json
    {
        "error": {
            "code": 404,
            "message": "visor not found"
        }
    }
    ```

### GET Dashboard

Returns aggregated dashboard statistics for the network.

**Request:**

```
GET /dashboard
```

**Response:**

- 200 OK (Success).
    ```json
    {
        "total_visors": 1500,
        "online_visors": 1200,
        "offline_visors": 300,
        "version_distribution": {
            "v2.0.0": 800,
            "v1.9.0": 400,
            "v1.8.0": 300
        },
        "uptime_stats": {
            "average": 95.5,
            "median": 98.0
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

### GET Security Nonce

Returns the current nonce for a visor (for signature verification).

**Request:**

```
GET /security/nonces/{pk}
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `pk` | string | Visor public key (hex) |

**Response:**

- 200 OK (Success).
    ```json
    {
        "edge": "02abc123...",
        "next_nonce": 42
    }
    ```

---

## Endpoint Summary

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/v4/update` | GET | SW-* headers | Register visor heartbeat |
| `/visors` | GET | No | List all registered visors |
| `/uptimes` | GET | No | Get all visor uptimes |
| `/uptime/{pk}` | GET | No | Get specific visor uptime |
| `/dashboard` | GET | No | Network dashboard stats |
| `/health` | GET | No | Health check |
| `/security/nonces/{pk}` | GET | No | Get visor nonce |
