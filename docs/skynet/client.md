# SkyNet client

The SkyNet client connects to a remote [SkyNet server](README.md) and
forwards the remote port to a local address, giving you access to a service
hosted on another Skywire visor as if it were running locally.

## Overview

The SkyNet client is a visor-native application that:

- Connects to a remote SkyNet server over a Skywire transport.
- Forwards a remote TCP port to a local address — protocol-agnostic, so it
  works for web servers, SSH, databases, and any other TCP service.
- Can run multiple instances at once.

## Usage

The client is controlled via `skywire cli skynet`.

### Start a client

```bash
# Connect to a server and forward the remote port to a local one
skywire cli skynet start --pk <server-public-key> --remote 8080 --local 9000

# A non-HTTP service (database, SSH, …) works the same way
skywire cli skynet start --pk <server-pk> --remote 3306 --local 3306

# With a custom instance name
skywire cli skynet start --pk <server-pk> --remote 8080 --local 9000 --name my-connection
```

Multi-hop and multiplexed-route options (`--min-hops`, `--routes`,
`--forward-mux` / `--reverse-mux`, …) are available too — see the
[command reference](../skywire/README.md).

### Check status

```bash
skywire cli skynet status
```

### Stop a client

```bash
skywire cli skynet stop --name skynet-client-9000
```

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
skywire cli skynet srv start --ports 8080

# Client side (your visor) — forward it to localhost:9000
SERVER_PK="02abc..."   # the server's public key
skywire cli skynet start --pk $SERVER_PK --remote 8080 --local 9000

# Now reach the remote server locally
curl http://localhost:9000
```

## See also

- [SkyNet server](README.md) — host a SkyNet service.
- [Command reference: `skywire cli skynet`](../skywire/README.md)
