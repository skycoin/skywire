# SkyNet Server

SkyNet server exposes local TCP ports over the Skywire network, enabling peer-to-peer port forwarding without transiting DMSG servers.

## Overview

The SkyNet server is a visor-native application that:
- Exposes local TCP ports to other Skywire visors
- Supports whitelist-based access control
- Works over STCPR, SUDPH, or DMSG transports
- Runs on the built-in visor forwarding port (47)

## Usage

The SkyNet server is controlled via `skywire cli skynet srv`:

### Start a Server

```bash
# Expose local port 8080
skywire cli skynet srv start --port 8080

# With custom name
skywire cli skynet srv start --port 3000 --name my-server

# With whitelist (restrict to specific public keys)
skywire cli skynet srv start --port 8080 --wl 02abc...,03def...
```

### Check Status

```bash
skywire cli skynet srv status
```

### Stop a Server

```bash
# By name
skywire cli skynet srv stop --name skynet-8080

# By port
skywire cli skynet srv stop --port 8080
```

## Configuration

SkyNet servers can be configured in `skywire-config.json` under the apps section:

```json
{
  "name": "skynet",
  "args": ["--port", "8080"],
  "auto_start": false,
  "port": 90
}
```

## How It Works

1. The server registers with the visor's built-in forwarding service on port 47
2. Remote clients connect via the skynet-client application
3. Traffic is forwarded between the remote client and the local TCP port
4. All communication is encrypted via Skywire's transport layer

## See Also

- [skynet-client](../skynet-client/README.md) - Client for connecting to SkyNet servers
- [Skywire README](../../../README.md) - Main documentation
