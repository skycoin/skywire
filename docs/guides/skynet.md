# Using SkyNet port forwarding

SkyNet enables peer-to-peer port forwarding over Skywire transports.
Unlike DmsgWeb which routes through DMSG servers, SkyNet uses direct
peer-to-peer connections (STCPR, SUDPH) when available.

## Running a SkyNet Server

To expose a local port over the Skywire network:

```bash
# Start a server exposing local port 8080
skywire cli skynet srv start --port 8080

# Start with a custom name
skywire cli skynet srv start --port 3000 --name my-web-server

# Restrict access to specific public keys (whitelist)
skywire cli skynet srv start --port 8080 --wl 02abc...,03def...
```

Check server status:
```bash
skywire cli skynet srv status
```

Stop a server:
```bash
skywire cli skynet srv stop --name skynet-8080
# Or stop by port
skywire cli skynet srv stop --port 8080
```

## Connecting with SkyNet Client

To forward a remote SkyNet server port to your localhost:

```bash
# Connect to a remote server and forward to local port
skywire cli skynet start --pk <server-public-key> --remote 8080 --local 9000

# Use raw TCP mode (for non-HTTP traffic)
skywire cli skynet start --pk <server-pk> --remote 3306 --local 3306 --raw-tcp
```

This makes the remote service available at `localhost:9000`.

Check client status:
```bash
skywire cli skynet status
```

Stop a client:
```bash
skywire cli skynet stop --name skynet-client-9000
```

## Example: Exposing a Web Server

On the server visor:
```bash
# Start a local web server (e.g., on port 8080)
python -m http.server 8080

# Expose it via SkyNet
skywire cli skynet srv start --port 8080
```

On the client visor:
```bash
# Get the server's public key
SERVER_PK="02abc..."

# Forward remote port 8080 to local port 9000
skywire cli skynet start --pk $SERVER_PK --remote 8080 --local 9000

# Access the remote web server locally
curl http://localhost:9000
```
