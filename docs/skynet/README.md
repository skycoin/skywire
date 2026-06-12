# SkyNet — port forwarding

SkyNet exposes a local TCP port on one visor and forwards it to localhost
on another, peer-to-peer over the Skywire network. Unlike a DMSG tunnel it
rides whatever transport the router selected — STCPR, SUDPH, or DMSG — so
traffic does not have to transit a DMSG relay.

SkyNet has two halves:

- **SkyNet server** (this page) — exposes a local port to the network.
- **[SkyNet client](client.md)** — connects to a server and forwards the
  remote port to a local address.

## Overview

The SkyNet server is a visor-native application that:

- Exposes a local TCP port to other Skywire visors.
- Supports whitelist-based access control by public key.
- Works over STCPR, SUDPH, or DMSG transports.
- Registers with the visor's built-in forwarding service (port `57`).

## Usage

The server is controlled via `skywire cli skynet srv`.

### Start a server

```bash
# Expose local port 8080
skywire cli skynet srv start --port 8080

# With a custom instance name
skywire cli skynet srv start --port 3000 --name my-server

# Restrict access to specific public keys (whitelist)
skywire cli skynet srv start --port 8080 --wl 02abc...,03def...
```

### Check status

```bash
skywire cli skynet srv status
```

### Stop a server

```bash
# By name
skywire cli skynet srv stop --name skynet-8080

# By port
skywire cli skynet srv stop --port 8080
```

## Configuration

A SkyNet server can also be declared in `skywire-config.json` under `apps`
so it starts with the visor:

```json
{
  "name": "skynet",
  "args": ["--port", "8080"],
  "auto_start": false,
  "port": 90
}
```

## How it works

1. The server registers with the visor's built-in forwarding service on
   port `57`.
2. A remote [SkyNet client](client.md) connects via a Skywire transport.
3. Traffic is forwarded between the remote client and the local TCP port.
4. All communication is encrypted by the Skywire transport layer.

## See also

- [SkyNet client](client.md) — connect to a SkyNet server.
- [Command reference: `skywire cli skynet`](../skywire/README.md)
