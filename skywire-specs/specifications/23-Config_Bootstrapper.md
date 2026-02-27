# Config Bootstrapper

The *Config Bootstrapper* (CB) is a service that provides initial configuration data to Skywire Visors during startup. It enables visors to discover service endpoints and network parameters without hardcoding them.

## Overview

The Config Bootstrapper provides:

- **Service endpoints**: URLs for uptime tracker, address resolver, transport discovery, etc.
- **DMSG configuration**: DMSG discovery URLs and server lists
- **Network parameters**: Default settings for transport limits, timeouts, etc.
- **DMSG HTTP configuration**: Configuration for HTTP-over-DMSG routing

## Code Structure

The code should be in the `skycoin/skywire-services` repository:

- `/cmd/config-bootstrapper/config-bootstrapper.go` is the main executable for the *Config Bootstrapper*.
- `/pkg/config-bootstrapper/api/` contains the RESTFUL API definitions.

## Database

The Config Bootstrapper is primarily a read-only service that serves static or semi-static configuration. Configuration is typically loaded from files or environment variables at startup.

## Endpoint Definitions

All endpoint calls should include an `Accept: application/json` field in the request header, and the response header should include a `Content-Type: application/json` field.

### GET Bootstrap Configuration

Returns the full bootstrap configuration for a visor.

**Request:**

```
GET /
```

**Response:**

- 200 OK (Success).
    ```json
    {
        "dmsg_discovery": "https://dmsgd.skywire.skycoin.com",
        "transport_discovery": "https://tpd.skywire.skycoin.com",
        "address_resolver": "https://ar.skywire.skycoin.com",
        "route_finder": "https://rf.skywire.skycoin.com",
        "uptime_tracker": "https://ut.skywire.skycoin.com",
        "service_discovery": "https://sd.skywire.skycoin.com",
        "stun_servers": [
            "stun.skywire.skycoin.com:3478",
            "stun2.skywire.skycoin.com:3478"
        ],
        "dmsg_servers": [
            {
                "pk": "02abc123...",
                "address": "dmsg1.skywire.skycoin.com:8081"
            },
            {
                "pk": "02def456...",
                "address": "dmsg2.skywire.skycoin.com:8081"
            }
        ],
        "default_settings": {
            "max_transports": 200,
            "transport_timeout": 30,
            "heartbeat_interval": 60
        }
    }
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `dmsg_discovery` | string | DMSG Discovery service URL |
| `transport_discovery` | string | Transport Discovery service URL |
| `address_resolver` | string | Address Resolver service URL |
| `route_finder` | string | Route Finder service URL |
| `uptime_tracker` | string | Uptime Tracker service URL |
| `service_discovery` | string | Service Discovery service URL |
| `stun_servers` | array | STUN server addresses for NAT traversal |
| `dmsg_servers` | array | Known DMSG servers with public keys |
| `default_settings` | object | Default visor configuration parameters |

### GET DMSG HTTP Configuration

Returns configuration for HTTP-over-DMSG routing.

**Request:**

```
GET /dmsghttp
```

**Response:**

- 200 OK (Success).
    ```json
    {
        "dmsg_servers": [
            {
                "pk": "02abc123...",
                "address": "dmsg1.skywire.skycoin.com:8081"
            }
        ],
        "dmsg_discovery": "https://dmsgd.skywire.skycoin.com",
        "http_routes": {
            "tpd.skywire.skycoin.com": {
                "pk": "03tpd123...",
                "port": 80
            },
            "ut.skywire.skycoin.com": {
                "pk": "03ut456...",
                "port": 80
            },
            "ar.skywire.skycoin.com": {
                "pk": "03ar789...",
                "port": 80
            }
        },
        "test_routes": {
            "test.skywire.skycoin.com": {
                "pk": "03test...",
                "port": 80
            }
        }
    }
    ```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `dmsg_servers` | array | DMSG servers for HTTP transport |
| `dmsg_discovery` | string | DMSG Discovery service URL |
| `http_routes` | object | HTTP hostnames mapped to DMSG addresses |
| `http_routes.*.pk` | string | Public key of the DMSG server hosting this route |
| `http_routes.*.port` | integer | Port number for the HTTP service |
| `test_routes` | object | Test/development HTTP routes |

### GET Specific Configuration

Returns a specific configuration section.

**Request:**

```
GET /config/{section}
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `section` | string | Configuration section: "services", "dmsg", "stun", "defaults" |

**Response:**

- 200 OK (Success - example for "services").
    ```json
    {
        "dmsg_discovery": "https://dmsgd.skywire.skycoin.com",
        "transport_discovery": "https://tpd.skywire.skycoin.com",
        "address_resolver": "https://ar.skywire.skycoin.com",
        "route_finder": "https://rf.skywire.skycoin.com",
        "uptime_tracker": "https://ut.skywire.skycoin.com",
        "service_discovery": "https://sd.skywire.skycoin.com"
    }
    ```

- 404 Not Found (Unknown section).
    ```json
    {
        "error": {
            "code": 404,
            "message": "unknown configuration section"
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
        "config_loaded_at": "2024-02-25T10:00:00Z",
        "config_version": "2024.02.25"
    }
    ```

---

## Configuration Sections

| Section | Description |
|---------|-------------|
| `services` | Service endpoint URLs |
| `dmsg` | DMSG servers and discovery |
| `stun` | STUN server addresses |
| `defaults` | Default visor parameters |

## Endpoint Summary

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/` | GET | No | Full bootstrap configuration |
| `/dmsghttp` | GET | No | DMSG HTTP routing configuration |
| `/config/{section}` | GET | No | Specific configuration section |
| `/health` | GET | No | Health check |
