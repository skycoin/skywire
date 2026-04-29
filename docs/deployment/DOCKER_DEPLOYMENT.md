# Skywire Docker Deployment

This document describes deploying skywire services using Docker Compose.

For systemd / direct-on-host deployment see [DEPLOYMENT_SYSTEMD.md](DEPLOYMENT_SYSTEMD.md). For Kubernetes notes (illustrative — no production K8s deployment exists today) see [KUBERNETES_DEPLOYMENT.md](KUBERNETES_DEPLOYMENT.md).

All services use the `skycoin/skywire:test` Docker image, which contains the unified skywire binary.

## Prerequisites

- Docker and Docker Compose
- A server with a public IP address
- For the STUN server: **two distinct public IP addresses** bound to the server's network interfaces (see [STUN Server](#stun-server))

### Minimum Server Specifications

- **CPU**: 2+ cores
- **RAM**: 4 GB minimum, 8 GB recommended
- **Storage**: 40 GB SSD
- **Network**: Public IPv4 address with unrestricted UDP (for STUN and SUDPH)
- **Ports**: The following ports must be accessible:
  - TCP: service ports (9090-9098, configurable via `.env`)
  - UDP: 30178 (address resolver SUDPH), 3478-3479 (STUN)
  - TCP: 30081+ (dmsg servers)

## Services Overview

The compose file defines the following services:

| Service | Command | Default Port | Dependencies | Description |
|---------|---------|-------------|--------------|-------------|
| address-resolver | `svc ar` | 9093 (TCP), 30178 (UDP) | redis, dmsg-discovery | Resolves visor addresses for STCPR/SUDPH transports |
| conf-service | `svc confbs` | configurable | none | Config bootstrap server for visor configuration |
| dmsg-discovery | `dmsg disc` | 9090 | redis | DMSG discovery server |
| dmsg-server | `dmsg server start` | 8080 (internal) | redis (if `enable_dht: true`) | DMSG relay server; optional DHT full node on dmsg port 100 |
| network-monitor | `svc nm` | configurable | most services | Monitors network health and cleans stale entries |
| route-finder | `svc rf` | 9092 | redis, dmsg-discovery, transport-discovery | Finds routes between visors |
| service-discovery | `svc sd` | 9098 | redis, dmsg-discovery | Service discovery (VPN servers, etc.) |
| setup-node | `svc sn` | none | dmsg-discovery | Route setup node |
| transport-discovery | `svc tpd` | 9091 | redis, dmsg-discovery | Transport discovery and registration |
| transport-setup | `svc tps` | none | none | Transport setup service |
| uptime-tracker | `svc ut` | configurable | postgres, redis, dmsg-discovery | Tracks visor uptime |
| stun | `svc stun` | 3478, 3479 | none | STUN server for NAT detection |
| geoip | `svc ip` | configurable | none | GeoIP service (embedded database) |
| postgres | postgres image | 5432 | none | Database for uptime tracker |
| redis | redis image | 6379 | none | Key-value store for service state |
| skywire-visor | `visor` | none | most services | Optional deployment visor |

## Configuration

### Environment File (.env)

All configuration is done through a `.env` file in the same directory as `compose.yaml`. A template is provided as `env.template` — copy it and fill in the values:

```
cp env.template .env
```

The `.env` file contains:
- Service ports
- Secret keys for each service (generate with `skywire cli config gen-keys`)
- Redis and Postgres credentials
- Network monitor public keys
- DMSG server configuration
- Pprof debug ports

### Key Generation

Generate a keypair for each service:

```
skywire cli config gen-keys
```

Each service that accepts `--sk` needs its own unique secret key.

### Config Bootstrapper

The config bootstrapper (`svc confbs`) serves deployment configuration to visors. It reads from a `config.json` file mounted at `/etc/skywire/config.json`. This file should contain the deployment's service URLs, STUN server addresses, and other configuration that visors need.

### DMSG Server Configuration

Each dmsg server requires its own `config.json` with a unique keypair. Generate with:

```
skywire dmsg server config gen -o dmsg-server-config.json
```

Update the `public_address` and `discovery` fields to match your deployment. Mount the config directory as a volume in the compose file.

#### DMSG Server DHT (optional, recommended for production)

Setting `enable_dht: true` and `redis_addr` in the dmsg-server's `config.json` turns the process into a Kademlia DHT full node on dmsg port 100, in addition to its normal dmsg relay duties. This is the serving layer that makes `dht get <pk> <salt>` from a visor return real data instead of falling through to HTTP discovery.

Example `config.json` for a dmsg-server colocated with the cluster's Redis (the primary host's compose):

```json
{
  "public_key": "0281a102c828...",
  "secret_key": "...",
  "discovery": "http://dmsg-discovery:9090",
  "public_address": "192.0.2.10:30086",
  "local_address": ":8080",
  "health_endpoint_address": ":8082",
  "max_sessions": 2048,
  "enable_dht": true,
  "redis_addr": "redis:6379"
}
```

For Docker Compose, the dmsg-server stanza must also pass `REDIS_PASSWORD`, `TPD_URL`, and `SD_URL` in its environment, and depend on `redis` and `dmsg-discovery`. The example in `compose.yaml` already does this.

How the DHT serves data when Redis is available:
- `redis_addr` points the DHT full node at the same Redis used by transport-discovery, dmsg-discovery, and service-discovery. Those services already write a Kademlia-shaped mirror of every entry into Redis under `dht:*` keys (see `pkg/dht/mirror_redis.go`); the dmsg-server's DHT node rehydrates from those keys on startup.
- The dmsg-servers form a bootstrap mesh by dialing each other's DHT port (over dmsg). The bootstrap PK list is built from the embedded `dmsg.Prod.DmsgServers`/`dmsg.Test.DmsgServers` list automatically, so visors and other dmsg-servers find each other without configuration.
- `TPD_URL` and `SD_URL` enable the DHT-to-discovery pusher: when a visor publishes its own DHT entry directly (e.g. `tp` or `svc` salts), the dmsg-server forwards that write back into the HTTP discoveries so they stay consistent.

#### Persistence options for `enable_dht: true`

The DHT backend is selected from the dmsg-server config in this order:

1. **`redis_addr` set** → Redis backend. Recommended for the primary host where the discovery services already write to the same Redis. The dmsg-server gets the full disc-mirror dataset on startup and serves it to visors via Kademlia. Requires `REDIS_PASSWORD` in the container environment.
2. **`redis_addr` empty, `persist_path` set** → bbolt backend. The dmsg-server keeps its own DHT state in a single file. State persists across restarts but the cluster's disc-mirror data (which lives in the primary Redis) is **not** reachable from this dmsg-server unless visor traffic happens to replicate it via Kademlia. Useful for off-host dmsg-servers that can't reach the primary's Redis but should still survive container restarts.
3. **Both empty** → in-memory only. Works fine, but every restart starts cold and the disc-mirror dataset is unreachable.

**Important Docker note for option 2 (bbolt):** the bbolt file path must point inside an existing volume mount, otherwise the file lives in the container's writable layer and is destroyed on `docker compose down && up`. The compose stanzas already mount `./dmsg-server/dmsg-server-N/` to `/etc/skywire/dmsg-server`, so the natural path is:

```json
{
  "enable_dht": true,
  "persist_path": "/etc/skywire/dmsg-server/dht.db"
}
```

That puts the bbolt file next to `config.json` on the host (`./dmsg-server/dmsg-server-N/dht.db`) and survives container recreation. No additional volume needed.

Without `enable_dht`, the dmsg-server works as a pure relay — visors rely on HTTP discovery for everything.

### Setup Node Configuration

The setup node requires a config file with a keypair and DMSG/transport discovery endpoints. Generate with:

```
skywire cli config gen --sn
```

## STUN Server

The STUN server implements RFC 3489 NAT type detection and **requires two distinct public IP addresses bound locally** on the server.

### Why Two IPs?

STUN NAT detection works by having the server respond from different IP addresses. The server binds four UDP sockets:
- primary IP : primary port (3478)
- primary IP : alt port (3479)
- secondary IP : primary port (3478)
- secondary IP : alt port (3479)

Both IPs must be configured on the server's network interfaces (`ip addr` should show both).

### Configuration

The STUN container uses `network_mode: "host"` to bind directly to the host's network interfaces:

```yaml
stun:
  container_name: stun
  image: "skycoin/skywire:test"
  network_mode: "host"
  restart: always
  command:
    - svc
    - stun
    - --primary-ip
    - "YOUR_PRIMARY_IP"
    - --secondary-ip
    - "YOUR_SECONDARY_IP"
    - --port
    - "3478"
    - --alt-port
    - "3479"
```

Verify your server has two public IPs with `ip addr`. If you only have one, you will need to add a secondary IP through your hosting provider.

### Verifying STUN

After starting the STUN server, verify it is working:

```
# Check that the process is listening
ss -ulnp | grep 3478

# Test from a remote machine
skywire cli config gen -r   # Should show NAT type in output
```

## GeoIP Service

The GeoIP service (`svc ip`) uses an embedded MaxMind GeoLite2-City database. No external database download is required:

```yaml
geoip:
  command:
    - svc
    - ip
    - --api
    - --addr
    - :${GEOIP_PORT}
    - --pprof
    - :${GEOIP_PPROF}
```

## Address Resolver UDP Port

The address resolver listens on both TCP (HTTP API) and UDP (SUDPH hole-punching). The UDP port (default 30178) must be mapped correctly:

```yaml
ports:
  - ${AR_PORT}:${AR_PORT}       # TCP: HTTP API
  - 30178:30178/udp             # UDP: SUDPH (must match internal port)
```

**Important**: The UDP port mapping must be `30178:30178/udp` (not `30178:${AR_PORT}/udp`), because the AR's internal KCP/UDP listener binds to port 30178 regardless of the HTTP port.

## Deployment

1. Copy `compose.yaml` and your `.env` file to the deployment server
2. Create required config directories and files:
   - `config-bootstrapper/config.json` — config bootstrapper service config
   - `dmsg-server/<name>/config.json` — dmsg server configs
   - `setup-node/config.json` — setup node config
   - `postgres-init/` — postgres initialization scripts
3. Pull the image and start services:

```bash
docker pull skycoin/skywire:test
docker compose up -d
```

4. Check service health:

```bash
docker compose ps
docker compose logs -f --tail 20
```

5. Verify individual services:

```bash
curl http://localhost:9090/health    # dmsg-discovery
curl http://localhost:9093/health    # address-resolver
curl http://localhost:9098/health    # service-discovery
```

## Reverse Proxy

For production deployments, place services behind a reverse proxy (e.g. Caddy, nginx) with TLS. Example Caddy configuration:

```
dmsgd.skywire.example.com {
  reverse_proxy http://127.0.0.1:9090
}
ar.skywire.example.com {
  reverse_proxy http://127.0.0.1:9093
}
rf.skywire.example.com {
  reverse_proxy http://127.0.0.1:9092
}
tpd.skywire.example.com {
  reverse_proxy http://127.0.0.1:9091
}
sd.skywire.example.com {
  reverse_proxy http://127.0.0.1:9098
}
```

Note: The STUN server and AR UDP port (30178) are not HTTP services and should not be proxied.

## Multiple Deployments

When running separate production and test deployments:

- Use the same Docker image (`skycoin/skywire:test`) on both to simplify image management
- Each deployment needs its own set of secret keys
- The STUN server can only run on servers with two public IPs
- The config bootstrapper on each deployment should point visors to the correct service URLs
- Use `-d <domain>` with `svc confbs` to set the deployment domain (e.g. `-d skywire.dev` for test)

## Troubleshooting

### STUN server restart loop
- Check `docker logs stun` — usually a port conflict with another process (e.g. old coturn)
- Run `ss -ulnp | grep 3478` to find conflicting processes

### SUDPH handshake timeout
- Verify the AR UDP port mapping is `30178:30178/udp`
- Check `docker logs address-resolver` for incoming UDP traffic
- Test UDP reachability: `echo "test" | nc -u -w5 <ar-host> 30178`

### Services not starting
- Check `.env` file exists and has all required variables
- Check `docker compose logs <service-name>`
- Verify redis and postgres are healthy: `docker compose ps`
