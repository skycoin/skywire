# Skynet Port Forwarding

Port forwarding over Skywire p2p routes (not DMSG).

## Overview

The `skynet` command provides port forwarding functionality over Skywire's network layer, allowing you to:
- **Client**: Connect to remote services exposed by other visors
- **Server**: Expose local services to be accessed by other visors

## Commands

### Client Mode: `skywire cli skynet`

Connect to remote services over Skywire routes.

**Usage:**
```bash
# Connect remote port to local port
skywire cli skynet <remote-pubkey> -r <remote-port> -p <local-port>

# List active connections
skywire cli skynet --ls

# Disconnect by connection ID
skywire cli skynet --stop <connection-id>
```

**Examples:**
```bash
# Forward remote port 8080 to local port 8080
skywire cli skynet 02c1e0bf...845 -r 8080 -p 8080

# Forward remote SSH (22) to local port 2222
skywire cli skynet 02c1e0bf...845 -r 22 -p 2222

# List all active forwarding connections
skywire cli skynet --ls

# Disconnect a specific connection
skywire cli skynet --stop f47ac10b-58cc-4372-a567-0e02b2c3d479
```

**Flags:**
- `-k, --pk <pubkey>` - Remote public key (alternative to positional arg)
- `-r, --remote <port>` - Remote port to connect to
- `-p, --port <port>` - Local port to listen on
- `-l, --ls` - List active connections
- `-d, --stop <id>` - Disconnect connection by ID

### Server Mode: `skywire cli skynet srv`

Expose local services to be accessed by remote visors.

**Usage:**
```bash
# Expose a local port
skywire cli skynet srv <port>

# Stop exposing a port
skywire cli skynet srv <port> --stop

# List exposed ports
skywire cli skynet srv --ls
```

**Examples:**
```bash
# Expose local web server on port 8080
skywire cli skynet srv 8080

# Expose SSH server
skywire cli skynet srv 22

# List all exposed ports
skywire cli skynet srv --ls

# Stop exposing port 8080
skywire cli skynet srv 8080 --stop
```

**Flags:**
- `-p, --port <port>` - Local port to expose (alternative to positional arg)
- `-d, --stop` - Stop exposing the specified port
- `-l, --ls` - List all exposed ports

## How It Works

### Server Side

1. Server visor registers a local port using `skynet srv <port>`
2. The port must be active (something listening on it)
3. Port becomes accessible to remote visors via Skywire routes

### Client Side

1. Client visor connects using `skynet <remote-pk> -r <remote-port> -p <local-port>`
2. Establishes route to remote visor over Skywire network
3. Creates local listener on specified port
4. Forwards traffic bidirectionally through the Skywire route

### Network Layer

- Uses `appnet.TypeSkynet` for routing (not DMSG)
- Establishes p2p routes through Skywire transport layer
- Leverages existing transport infrastructure (STCP, SUDPH, DMSG)

## Comparison with `fwd` and `rev`

The new `skynet` command consolidates functionality from the older `fwd` and `rev` commands:

| Old Commands | New Command | Functionality |
|-------------|-------------|---------------|
| `skywire cli fwd` | `skywire cli skynet srv` | Expose local ports |
| `skywire cli rev` | `skywire cli skynet` | Connect to remote ports |

**Migration:**

```bash
# Old: skywire cli fwd 8080
# New:
skywire cli skynet srv 8080

# Old: skywire cli rev 02abc... -r 8080 -p 8080
# New:
skywire cli skynet 02abc... -r 8080 -p 8080
```

## Port Requirements

- **Valid range**: 1-65535
- **Port 0**: Reserved, not allowed
- **Server**: Port must have active listener before registration
- **Client**: Local port must be available (not in use)

## Connection Management

Connections are identified by UUID and persist until:
- Explicitly disconnected using `--stop`
- Visor restarts
- Remote endpoint becomes unavailable

## Troubleshooting

**"Port already in use"**
- Check if another process is using the local port: `netstat -tulpn | grep <port>`
- Try a different local port

**"No connection on local port"**
- Ensure a service is listening on the port before using `skynet srv`
- Verify with: `netstat -tulpn | grep <port>`

**"Server closed with error"**
- Remote port may not be registered
- Remote visor may be offline
- Check remote visor logs for details

## See Also

- `skywire cli tp` - Transport management
- `skywire cli visor` - Visor management
- `skywire cli route` - Route management
