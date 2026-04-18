# Skynet Port Forwarding

Forward local TCP ports over the Skywire network (skynet and/or DMSG).
Remote visors can connect to forwarded ports via skynet routes or DMSG.

## Managing Forwarded Ports

### Add a port

```bash
skywire cli skynet port add <port> [flags]
```

Flags:
- `-l, --label` — human-readable label (shown on landing page)
- `-d, --desc` — description
- `--skynet` — forward over skynet (default: true)
- `--dmsg` — forward over DMSG (default: true)
- `--landing` — show link on visor landing page (default: true)
- `--proxy-addr` — reverse proxy to a local address (e.g., `127.0.0.1:3000`); for port 80, replaces the landing page

Examples:

```bash
# Forward port 8080 with defaults
skywire cli skynet port add 8080

# Forward with metadata
skywire cli skynet port add 8080 --label "My App" --desc "Web dashboard"

# Forward over skynet only (no DMSG)
skywire cli skynet port add 3000 --dmsg=false

# Host a website on port 80 (replaces landing page)
skywire cli skynet port add 80 --proxy-addr 127.0.0.1:3000 --label "My Website"
```

### Remove a port

```bash
skywire cli skynet port rm <port>
```

### List forwarded ports

```bash
skywire cli skynet port ls
```

Output columns: PORT, LABEL, SKYNET, DMSG, LANDING, DESCRIPTION

## Hosting a Website

To serve a static website over skynet/DMSG on your visor's port 80:

```bash
# Start a local file server
skywire cli util serve /path/to/site
# Output: Serving /path/to/site on http://127.0.0.1:43210

# Forward port 80 to it
skywire cli skynet port add 80 --proxy-addr 127.0.0.1:43210 --label "My Site"
```

The visor's system endpoints (`/health`, `/node-info`, `/services`) are
always accessible — the website only replaces unmatched routes.

## Access Control

Forwarded ports support a PK whitelist. When set, only visors with
listed public keys can access the port. An empty whitelist means
the port is accessible to all authenticated peers.

Port 80 has three tiers of access control:
- `/health`, `/services` — open to everyone
- `/node-info`, `/visor.log`, `/debug/pprof` — survey whitelist
- Website (everything else) — forwarded port whitelist

## Making HTTP Requests Over Skynet

```bash
skywire cli skynet curl skynet://<public-key>/path
skywire cli skynet curl skynet://<public-key>:<port>/path
skywire cli skynet curl -d '{"key":"val"}' skynet://<public-key>/endpoint
skywire cli skynet curl -o output.file skynet://<public-key>/large-file
```

## Persistence

Forwarded port configuration is stored in `local/forwarded_ports.json`
and persists across visor restarts. It is separate from the visor
config file.

## RPC Integration

Applications can manage forwarded ports programmatically via the visor's
RPC interface:

```go
rpcClient.RegisterForwardedPort(visor.ForwardedPort{
    Port:          8080,
    Label:         "My App",
    Skynet:        true,
    DMSG:          true,
    ShowOnLanding: true,
})

rpcClient.DeregisterTCPPort(8080)

ports, _ := rpcClient.ListForwardedPorts()
```
