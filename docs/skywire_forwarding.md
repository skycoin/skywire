# Serving Localhost Ports Over Skywire

Expose local TCP ports over the Skywire network (skynet and/or DMSG).
Remote visors can connect via skynet routes or DMSG.

This is the generic recipe for running **any** app over skynet/dmsg — no
public IP, no DNS, no CA. Two halves:

1. **Far end** — the host running the app forwards its port (`serve add`),
   making it reachable at `<base32-pk>.dmsg` / `.skynet`. For HTTP you can
   forward straight to the app (built-in reverse-proxy) or via a real reverse
   proxy like Caddy for multiple vhosted sites — [see below](#hosting-a-website).
2. **Near end** — the client reaches it with `skywire cli got`, or points a
   browser at the [resolving proxy](#reaching-a-served-app) and visits the
   `.dmsg` / `.skynet` hostname.

As long as the far end has forwarded the port, the near end just needs the
resolving proxy and the hostname.

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

### Without a reverse proxy — one app, no extra software

The example above is the zero-dependency path: the visor has a **built-in
port-80 HTTP reverse-proxy**, so a single site needs nothing but the one
`serve add` command. The visor forwards port-80 HTTP straight to your local
app. Good for a single dashboard, API, or static site.

### Many sites, HTTPS, per-PK auth → "websites over skywire"

For **more than one site**, `Host`/path routing, HTTPS, or a backend that needs
the caller's identity, run a normal reverse proxy (Caddy / nginx) on the host
and forward the visor's port 80 to it with `--preserve-host` so it sees the
vhost the visitor asked for:

```bash
skywire cli serve add 80 --to 127.0.0.1:80 --preserve-host --label "sites"
```

That's a whole topic of its own — **websites over skywire** — covered in depth
elsewhere so it isn't re-explained here:

- **[PK-aware websites over skynet / dmsg](guides/skynet-website-auth.md)** —
  the Caddy config, serving on skynet + clearnet at once, `inject_pk`, and the
  trust boundary. (`node.skycoin.com` serves its whole stack — node API,
  explorer, blog — from one visor this way.)
- **[Resolver TLS mode](skynet-tls.md)** — HTTPS for `.dmsg`/`.skynet` browser
  requests, and the base32 hostname format.

Rule of thumb: **one HTTP app → the built-in proxy** above; **many sites /
HTTPS / auth → a reverse proxy** and those two guides.

## Access Control

Served ports support a PK whitelist. When set, only visors with listed
public keys can access the port. Empty whitelist = open to all
authenticated peers.

Port 80 has three tiers of access control:
- `/health`, `/services` — open to everyone
- `/node-info`, `/visor.log`, `/debug/pprof` — survey whitelist
- Website (everything else) — the served port's `--whitelist`

## Reaching a Served App

Once a port is served, any peer visor can reach it. There's nothing
app-specific about this — it's the same for a website, an API, a database,
or SSH.

### From the CLI

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

### From a browser (or any app) — the resolving proxy

Run the resolving proxy and point the browser (or any SOCKS-aware app) at
it; then use a **readable mesh hostname** and browse `.dmsg` / `.skynet`
domains directly — no IP, no DNS, no CA:

```bash
# A resolving SOCKS5 proxy that dials <pk>.dmsg / <pk>.skynet over the mesh.
skywire dmsg web

# Point the browser at it, e.g. socks5h://127.0.0.1:<port>  (socks5h so the
# PROXY resolves the hostname — the browser must not try to). Then visit:
#   http://<base32-pk>.dmsg/            (a bare visor)
#   http://<name>.<base32-pk>.dmsg/     (a vhost behind a reverse proxy)
```

The `<base32-pk>` label is the DNS-safe form of a visor's public key:

```bash
skywire cli visor pk dnslabel <hex-pk>
# → aong2hr4en7v6bnxr3az5hzruad7qsbv27xr5ajioyicfaor3n2mc
# so node.skycoin.com is reachable at
#   node.skycoin.com.aong2hr4en7v6bnxr3az5hzruad7qsbv27xr5ajioyicfaor3n2mc.dmsg
```

The wasm-visor's built-in iframe browser does this in-tab (see
[the skynet browser](skynet-browser.md)); a native desktop browser uses the
standalone resolving proxy above. For HTTPS over the mesh, see
[Resolver TLS mode](skynet-tls.md).

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
