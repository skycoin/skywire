# SkyNet Client

SkyNet client connects to remote SkyNet servers and forwards their ports to localhost, enabling access to services hosted on other Skywire visors.

## Overview

The SkyNet client is a visor-native application that:
- Connects to remote SkyNet servers via Skywire transports
- Forwards remote ports to local addresses
- Supports both HTTP and raw TCP forwarding modes
- Can run multiple instances simultaneously

## Usage

The SkyNet client is controlled via `skywire cli skynet`:

### Start a Client

```bash
# Connect to remote server and forward to local port
skywire cli skynet start --pk <server-public-key> --remote 8080 --local 9000

# Use raw TCP mode (for non-HTTP traffic like databases, SSH, etc.)
skywire cli skynet start --pk <server-pk> --remote 3306 --local 3306 --raw-tcp

# With custom name
skywire cli skynet start --pk <server-pk> --remote 8080 --local 9000 --name my-connection
```

### Check Status

```bash
skywire cli skynet status
```

### Stop a Client

```bash
# By name
skywire cli skynet stop --name skynet-client-9000
```

## Configuration

SkyNet clients can be configured in `skywire-config.json` under the apps section:

```json
{
  "name": "skynet-client",
  "args": ["--pk", "02abc...", "--remote", "8080", "--local", "9000"],
  "auto_start": false,
  "port": 47
}
```

## Forwarding Modes

### HTTP Mode (default)
Best for web servers and HTTP-based services. Handles request-response patterns efficiently.

### Raw TCP Mode (`--raw-tcp`)
Best for:
- Database connections (MySQL, PostgreSQL)
- SSH tunneling
- Streaming protocols
- Any non-HTTP TCP traffic

## Example: Accessing a Remote Web Server

```bash
# On the server side (remote visor)
# Start a local web server and expose it via SkyNet
python -m http.server 8080
skywire cli skynet srv start --port 8080

# On the client side (your visor)
SERVER_PK="02abc..."  # Server's public key
skywire cli skynet start --pk $SERVER_PK --remote 8080 --local 9000

# Access the remote server locally
curl http://localhost:9000
```

## See Also

- [skynet](../skynet/README.md) - Server for hosting SkyNet services
- [Skywire README](../../../README.md) - Main documentation
