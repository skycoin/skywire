# Network Monitor

The *Network Monitor* (NM) is a service that monitors the health and status of the Skywire network infrastructure. It performs periodic checks on visors, services, and transports to ensure network reliability.

## Overview

The Network Monitor:

- **Monitors visor health**: Checks if visors are responsive
- **Tracks service availability**: Monitors uptime tracker, address resolver, etc.
- **Detects network issues**: Identifies connectivity problems
- **Provides network status**: Aggregates health information for dashboards

## Code Structure

The code should be in the `skycoin/skywire-services` repository:

- `/cmd/network-monitor/network-monitor.go` is the main executable for the *Network Monitor*.
- `/pkg/network-monitor/api/` contains the RESTFUL API definitions.
- `/pkg/network-monitor/store/` contains the monitoring data storage.

## Authentication

The Network Monitor uses its own authentication scheme for privileged operations:

**Request Headers:**

| Header | Description |
|--------|-------------|
| `NM-PK` | Network Monitor's public key |
| `NM-Sign` | Signature of the request body |

Most read-only endpoints are publicly accessible.

## Endpoint Definitions

All endpoint calls should include an `Accept: application/json` field in the request header, and the response header should include a `Content-Type: application/json` field.

### GET Network Status

Returns the current status of the Skywire network.

**Request:**

```
GET /status
```

**Response:**

- 200 OK (Success).
    ```json
    {
        "network": {
            "healthy": true,
            "visors": {
                "total": 1500,
                "online": 1200,
                "offline": 300,
                "percentage_online": 80.0
            },
            "transports": {
                "total": 5000,
                "active": 4500,
                "stale": 500
            }
        },
        "services": {
            "uptime_tracker": {
                "status": "healthy",
                "latency_ms": 45,
                "last_check": "2024-02-25T10:30:00Z"
            },
            "address_resolver": {
                "status": "healthy",
                "latency_ms": 32,
                "last_check": "2024-02-25T10:30:00Z"
            },
            "transport_discovery": {
                "status": "healthy",
                "latency_ms": 28,
                "last_check": "2024-02-25T10:30:00Z"
            },
            "route_finder": {
                "status": "healthy",
                "latency_ms": 55,
                "last_check": "2024-02-25T10:30:00Z"
            },
            "dmsg_discovery": {
                "status": "healthy",
                "latency_ms": 38,
                "last_check": "2024-02-25T10:30:00Z"
            }
        },
        "dmsg_servers": {
            "total": 10,
            "available": 9,
            "servers": [
                {
                    "pk": "02abc123...",
                    "address": "dmsg.server1.example.com:8081",
                    "status": "available",
                    "clients": 150
                }
            ]
        },
        "timestamp": "2024-02-25T10:30:00Z"
    }
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `network.healthy` | boolean | Overall network health status |
| `network.visors` | object | Visor statistics |
| `network.transports` | object | Transport statistics |
| `services` | object | Service health status for each service |
| `services.*.status` | string | "healthy", "degraded", or "down" |
| `services.*.latency_ms` | integer | Response latency in milliseconds |
| `dmsg_servers` | object | DMSG server availability |
| `timestamp` | string | Status check timestamp (RFC3339) |

### GET Visor Status

Returns detailed status for a specific visor.

**Request:**

```
GET /status/visor/{pk}
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
        "version": "v2.0.0",
        "transports": {
            "count": 5,
            "types": {
                "stcpr": 2,
                "sudph": 2,
                "dmsg": 1
            }
        },
        "last_seen": "2024-02-25T10:30:00Z",
        "ip": "192.168.1.1",
        "geo": {
            "country": "US",
            "city": "New York"
        }
    }
    ```

- 404 Not Found (Visor not found).
    ```json
    {
        "error": {
            "code": 404,
            "message": "visor not found"
        }
    }
    ```

### GET Service Status

Returns detailed status for a specific service.

**Request:**

```
GET /status/service/{name}
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `name` | string | Service name (e.g., "uptime_tracker", "address_resolver") |

**Response:**

- 200 OK (Success).
    ```json
    {
        "name": "uptime_tracker",
        "status": "healthy",
        "url": "https://ut.skywire.skycoin.com",
        "checks": [
            {
                "endpoint": "/health",
                "status": 200,
                "latency_ms": 45,
                "checked_at": "2024-02-25T10:30:00Z"
            },
            {
                "endpoint": "/uptimes",
                "status": 200,
                "latency_ms": 120,
                "checked_at": "2024-02-25T10:30:00Z"
            }
        ],
        "uptime_24h": 99.9,
        "incidents": []
    }
    ```

### GET Health Check

Returns the Network Monitor's own health status.

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
        "monitoring": {
            "services_monitored": 5,
            "visors_tracked": 1500,
            "check_interval_seconds": 60
        }
    }
    ```

### GET Alerts

Returns current network alerts and incidents.

**Request:**

```
GET /alerts
```

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `severity` | string | No | Filter by severity: "critical", "warning", "info" |
| `active` | boolean | No | Only show active alerts (default: true) |
| `limit` | integer | No | Maximum results (default: 50) |

**Response:**

- 200 OK (Success).
    ```json
    {
        "alerts": [
            {
                "id": "alert-123",
                "severity": "warning",
                "type": "service_degraded",
                "service": "dmsg_discovery",
                "message": "DMSG Discovery response time elevated",
                "started_at": "2024-02-25T10:00:00Z",
                "resolved_at": null,
                "active": true
            }
        ],
        "summary": {
            "critical": 0,
            "warning": 1,
            "info": 3
        }
    }
    ```

---

## Monitored Services

| Service | Health Endpoint | Check Interval |
|---------|----------------|----------------|
| Uptime Tracker | `/health` | 60s |
| Address Resolver | `/health` | 60s |
| Transport Discovery | `/health` | 60s |
| Route Finder | `/health` | 60s |
| DMSG Discovery | `/dmsg-discovery/available_servers` | 60s |
| Service Discovery | `/health` | 60s |

## Endpoint Summary

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/status` | GET | No | Overall network status |
| `/status/visor/{pk}` | GET | No | Specific visor status |
| `/status/service/{name}` | GET | No | Specific service status |
| `/health` | GET | No | Network Monitor health |
| `/alerts` | GET | No | Active alerts and incidents |
