# Skywire Docker Deployment

This document describes deploying skywire services using Docker Compose.

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
| dmsg-server | `dmsg server start` | 8080 (internal) | none | DMSG relay server |
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
