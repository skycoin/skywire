# Serving Localhost Ports Over Skywire

Expose local TCP ports over the Skywire network (skynet and/or DMSG).
Remote visors can connect via skynet routes or DMSG.

The CLI tree lives under `skywire cli serve`. The legacy
`skywire cli skynet port {add,ls,rm}` commands still work but are
deprecated.

## Managing Served Ports

### Add a port

```bash
skywire cli serve add <port> [flags]
```

Flags:
- `--to <host:port|port>` — local target (`127.0.0.1:9883`, or just
  `9883` → `localhost:9883`). Required for non-default forwarding.
- `-l, --label` — label shown on the visor landing page
- `-d, --desc` — description on the landing page
- `--skynet` / `--dmsg` — expose over each network face (default: both)
- `--landing` — show a link on the visor landing page (default: true)
- `--whitelist` — comma-separated PKs allowed to access the port
  (empty = allow all peers)

Examples:

```bash
# Forward port 8080 with defaults (skynet + DMSG, landing-page link)
skywire cli serve add 8080 --to 8080

# Same thing with metadata
skywire cli serve add 8080 --to 8080 --label "My App" --desc "Web dashboard"

# Skynet only
skywire cli serve add 3000 --to 3000 --dmsg=false

# Host a website on port 80 (HTTP reverse-proxy through the
# landing-page handler — replaces the visor's default landing page)
skywire cli serve add 80 --to 127.0.0.1:3000 --label "My Website"
```

`--to` accepts either `host:port` (only `127.0.0.1` / `localhost` for raw
TCP forwarding) or a bare port. Port 80 wires up an HTTP reverse-proxy
through the dmsghttp logserver; every other port is raw TCP.

### Remove a port

```bash
skywire cli serve rm <port>
```

### List served ports

```bash
skywire cli serve            # bare command lists (default action)
skywire cli serve ls         # explicit alias
```

Output columns: `PORT`, `TO`, `LABEL`, `SKYNET`, `DMSG`, `LANDING`,
`DESCRIPTION`.

## Hosting a Website

To serve a static website on your visor's port 80:

```bash
# Start a local file server (the static-file helper)
skywire cli util serve /path/to/site
# Output: Serving /path/to/site on http://127.0.0.1:43210

# Reverse-proxy port 80 to it
skywire cli serve add 80 --to 127.0.0.1:43210 --label "My Site"
```

The visor's system endpoints (`/health`, `/node-info`, `/services`) are
always reachable — the website only replaces unmatched routes.

> **Note (WebSockets):** the port-80 reverse-proxy serves plain HTTP
> requests via the dmsghttp logserver. WebSocket upgrades are not
> currently fully supported on port 80; if your app needs WebSockets,
> serve it on its native port (e.g. `serve add 8085 --to 8085`) and let
> clients connect over `.skynet:8085` / `.dmsg:8085` directly.

## Access Control

Served ports support a PK whitelist. When set, only visors with listed
public keys can access the port. Empty whitelist = open to all
authenticated peers.

Port 80 has three tiers of access control:
- `/health`, `/services` — open to everyone
- `/node-info`, `/visor.log`, `/debug/pprof` — survey whitelist
- Website (everything else) — the served port's `--whitelist`

## Making HTTP Requests Over Skynet / DMSG

`skywire cli got` is a unified HTTP client that speaks `http://`,
`https://`, `skynet://` and `dmsg://`. The skywire schemes are
routed through the local visor's RPC.

```bash
skywire cli got skynet://<public-key>/path
skywire cli got skynet://<public-key>:<port>/path
skywire cli got dmsg://<public-key>:<port>/path
skywire cli got req POST skynet://<public-key>/endpoint -D '{"key":"val"}'
skywire cli got dl skynet://<public-key>/large-file -o output.file
```

## Persistence

Served-port configuration lives in `local/forwarded_ports.json` and
persists across visor restarts. It is separate from the visor config
file.

## RPC Integration

Applications can manage served ports programmatically:

```go
rpcClient.RegisterForwardedPort(visor.ForwardedPort{
    Port:          8080,
    LocalPort:     8080,
    Label:         "My App",
    Skynet:        true,
    DMSG:          true,
    ShowOnLanding: true,
})

rpcClient.DeregisterTCPPort(8080)

ports, _ := rpcClient.ListForwardedPorts()
```
