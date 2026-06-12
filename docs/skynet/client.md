# SkyNet client

The SkyNet client connects to a remote [SkyNet server](README.md) and
forwards the remote port to a local address, giving you access to a service
hosted on another Skywire visor as if it were running locally.

## Overview

The SkyNet client is a visor-native application that:

- Connects to a remote SkyNet server over a Skywire transport.
- Forwards a remote port to a local address.
- Supports both HTTP and raw-TCP forwarding modes.
- Can run multiple instances at once.

## Usage

The client is controlled via `skywire cli skynet`.

### Start a client

```bash
# Connect to a server and forward the remote port to a local one
skywire cli skynet start --pk <server-public-key> --remote 8080 --local 9000

# Raw-TCP mode (databases, SSH, streaming — any non-HTTP traffic)
skywire cli skynet start --pk <server-pk> --remote 3306 --local 3306 --raw-tcp

# With a custom instance name
skywire cli skynet start --pk <server-pk> --remote 8080 --local 9000 --name my-connection
```

### Check status

```bash
skywire cli skynet status
```

### Stop a client

```bash
skywire cli skynet stop --name skynet-client-9000
```

## Forwarding modes

### HTTP mode (default)

Best for web servers and HTTP-based services; handles request/response
patterns efficiently.

### Raw-TCP mode (`--raw-tcp`)

Best for:

- Database connections (MySQL, PostgreSQL)
- SSH tunnelling
- Streaming protocols
- Any non-HTTP TCP traffic

## Configuration

A SkyNet client can be declared in `skywire-config.json` under `apps`:

```json
{
  "name": "skynet-client",
  "args": ["--pk", "02abc...", "--remote", "8080", "--local", "9000"],
  "auto_start": false,
  "port": 57
}
```

## Example: accessing a remote web server

```bash
# Server side (remote visor) — expose a local web server
python -m http.server 8080
skywire cli skynet srv start --port 8080

# Client side (your visor) — forward it to localhost:9000
SERVER_PK="02abc..."   # the server's public key
skywire cli skynet start --pk $SERVER_PK --remote 8080 --local 9000

# Now reach the remote server locally
curl http://localhost:9000
```

## See also

- [SkyNet server](README.md) — host a SkyNet service.
- [Command reference: `skywire cli skynet`](../skywire/README.md)
