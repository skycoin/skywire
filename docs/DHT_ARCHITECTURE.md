# DHT Architecture

## Overview

The Skywire DHT uses Kademlia with BEP44 mutable data semantics. Data is namespaced by salt (e.g., `"dmsg"` for DMSG discovery entries, `"tp"` for transports, `"svc"` for services).

## Node Types

### DMSG Servers (DHT Full Nodes)

DMSG servers are the primary DHT infrastructure. Every visor already has DMSG sessions to them, so they're natural bootstrap peers with zero extra connections.

- Enabled via `"enable_dht": true` in the DMSG server config
- Listen on DMSG port 100 for Kademlia RPC
- Store all DHT items (full node mode)
- Advertise `"dht_bootstrap": true` in their DMSG discovery entry
- Visible in `/available_servers` so visors know which servers to bootstrap from

### Visors (DHT Regular Nodes)

Visors participate in the DHT as regular nodes. They store only items near their XOR distance and help route queries.

- DHT starts automatically when DMSG is available
- Bootstrap from DMSG servers marked as `dht_bootstrap`
- Publish their own DMSG entry and transport list every 60 seconds
- Can enable full node mode at runtime: `skywire cli visor dht full-node on`

### Deployment Services (No DHT Node)

Deployment services (DMSG discovery, transport discovery, service discovery, etc.) do NOT run DHT nodes. They mirror their data into the DHT via Redis.

- `DisableDHT: true` in their svcmode configuration
- Mirror entries to Redis on every SetEntry/RegisterTransport/PostService call
- DMSG servers read from the same Redis and serve the data via Kademlia

## Data Persistence

### Redis (Shared Store)

When multiple processes on the same machine need to share DHT data, they all point to the same Redis instance. Redis handles concurrent reads/writes natively.

```json
{
  "dht": {
    "redis_addr": "redis:6379"
  }
}
```

- DMSG servers: use Redis as DHT backend (read + write via Kademlia)
- Deployment services: write directly to Redis (same key format: `dht:<target_hex>`)
- Result: one dataset, multiple access points

### bbolt (Per-Process Store)

When Redis is not available, each process uses its own bbolt file. No sharing between processes — Kademlia handles synchronization.

```json
{
  "dht": {
    "persist_path": "/data/dht.db"
  }
}
```

- Suitable for standalone visors or single-service deployments
- Data persists across restarts
- bbolt supports only one writer per file — do NOT share the same file between processes

### In-Memory (No Persistence)

When neither Redis nor bbolt is configured, the DHT store is in-memory only. Data is lost on restart and must be re-synced from the network.

## Deployment Topology

### Single-Host Deployment (Production)

```
┌─────────────────────────────────────────────────────────┐
│  Host                                                    │
│                                                          │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐   │
│  │ DMSG Server 1│  │ DMSG Server 6│  │ DMSG Server 7│   │
│  │ DHT Full Node│  │ DHT Full Node│  │ DHT Full Node│   │
│  │ ↕ Redis      │  │ ↕ Redis      │  │ ↕ Redis      │   │
│  └──────────────┘  └──────────────┘  └──────────────┘   │
│         ↕                 ↕                 ↕            │
│  ┌──────────────────────────────────────────────────┐   │
│  │                    Redis                          │   │
│  │              (shared DHT store)                   │   │
│  └──────────────────────────────────────────────────┘   │
│         ↑                 ↑                 ↑            │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐   │
│  │ DMSG Disc    │  │ Transport Disc│  │ Service Disc │   │
│  │ Mirror→Redis │  │ Mirror→Redis │  │ Mirror→Redis │   │
│  │ (no DHT node)│  │ (no DHT node)│  │ (no DHT node)│   │
│  └──────────────┘  └──────────────┘  └──────────────┘   │
└─────────────────────────────────────────────────────────┘
```

- 3 Kademlia nodes (different PKs, different sessions, same Redis)
- 1 Redis instance (one dataset)
- 3 deployment services mirror to Redis directly (no DHT overhead)

### Visor + DMSG Server (Home Setup)

```
┌─────────────────────────────────┐
│  Machine                         │
│                                  │
│  ┌──────────┐  ┌──────────────┐ │
│  │  Visor   │  │ DMSG Server  │ │
│  │ DHT Node │  │ DHT Full Node│ │
│  │ ↕ bbolt  │  │ ↕ bbolt      │ │
│  └──────────┘  └──────────────┘ │
└─────────────────────────────────┘
```

- No Redis needed — each process uses its own bbolt file
- Kademlia syncs data between them over DMSG port 100
- Two separate datasets that converge via Kademlia protocol

With Redis:

```
┌─────────────────────────────────┐
│  Machine                         │
│                                  │
│  ┌──────────┐  ┌──────────────┐ │
│  │  Visor   │  │ DMSG Server  │ │
│  │ DHT Node │  │ DHT Full Node│ │
│  │ ↕ Redis  │  │ ↕ Redis      │ │
│  └──────────┘  └──────────────┘ │
│         ↕            ↕           │
│  ┌──────────────────────────┐   │
│  │         Redis             │   │
│  └──────────────────────────┘   │
└─────────────────────────────────┘
```

- One dataset (Redis), two Kademlia nodes
- No data duplication

## Configuration Reference

### DMSG Server

```json
{
  "enable_dht": true,
  "redis_addr": "redis:6379"
}
```

### Visor

```json
{
  "dht": {
    "full_node": false,
    "persist_path": "/data/dht.db",
    "redis_addr": "",
    "redis_password": "",
    "redis_db": 0
  }
}
```

### Deployment Services

No DHT configuration needed — they use `DisableDHT: true` by default. Mirroring happens automatically via Redis when the service has Redis access.

## Data Flow

1. **Visor publishes** its DMSG entry to the DHT (every 60s via Kademlia PutValue)
2. **Old visors** publish to HTTP (DMSG discovery, TPD, SD)
3. **Deployment services** mirror HTTP data to Redis in DHT format
4. **DMSG servers** read from Redis and serve via Kademlia
5. **Visors query** DHT first (via Kademlia), fall back to HTTP

## Bootstrap

1. Visor starts, checks DMSG discovery for servers with `dht_bootstrap: true`
2. Dials those servers on DMSG port 100
3. If discovery lookup fails (server entries aren't client entries), falls back to dialing through existing DMSG sessions
4. Bootstrap retries every 30 seconds until peers are found, then every 5 minutes
