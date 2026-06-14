# The `.dmsg` / `.skynet` resolving proxy

`skywire cli resolver` runs two embedded **resolving SOCKS5 proxies** inside the
visor. They let any SOCKS5-aware program — a browser, `curl`, etc. — reach
skywire peers and deployment services **by public key, over dmsg or skynet**,
with no clearnet hop and no per-target configuration:

- **dmsgweb** — resolves `*.dmsg` hostnames and tunnels them straight over the
  visor's dmsg client. Default listener: `127.0.0.1:4445`.
- **skynetweb** — resolves `*.skynet` hostnames over skywire transports (the
  same routes `cli skynet` port-forwarding uses). Default listener:
  `127.0.0.1:4446`.

By default `resolver up` brings both up and **chains dmsgweb → skynetweb**, so a
single browser/curl proxy entry at `127.0.0.1:4445` covers `.dmsg`, `.skynet`,
and — with `--upstream` — everything else too.

> Since the CLI no longer falls back to plain HTTP for deployment services (they
> are reached over dmsg only), the resolving proxy is the supported way to view a
> deployment service's data in a browser or with `curl`.

## Enable / disable / status

```
skywire cli resolver            # status of both resolvers
skywire cli resolver up         # enable both (dmsgweb chained to skynetweb)
skywire cli resolver up --dmsg-only
skywire cli resolver up --upstream 127.0.0.1:1080   # send other traffic to a SOCKS5 (e.g. skysocks)
skywire cli resolver down       # disable both
```

To enable them at config-generation / autoconfig time instead (writes
`DMSGWEB=true` / `SKYNETWEB=true` into `skywire.conf`):

```
skywire cli config gen --dmsgweb --skynetweb ...
# or, on an installed system:
sudo skywire autoconfig --dmsgweb --skynetweb
```

## Addressing

A resolver hostname is `<public-key>.<suffix>:<port>/<path>`:

- `<public-key>` — the 66-hex-char public key of the target visor or service.
- `:<port>` — the **dmsg port** the target serves HTTP on. **Deployment services
  serve their API on dmsg port `80`.**
- `.dmsg` addresses may carry routing prefixes, e.g. `<server-pk>.<dest-pk>.dmsg`
  pins a specific dmsg rendezvous server. Use `cli tp route-addr` to build one.

### Named sites / virtual hosts

A label to the **left** of the destination PK that is *not* itself PK- or
TpID-shaped becomes a **vhost** — the resolver rewrites the outgoing HTTP `Host:`
header to it. So:

```
http://example.com.<visor-pk>.skynet/
```

dials `<visor-pk>`'s port 80 and sends `Host: example.com`. If that visor forwards
its port 80 to a reverse proxy (Caddy/nginx/traefik) that dispatches by Host, the
proxy serves the `example.com` vhost — letting **one visor expose many real-domain
sites** over skynet (or dmsg). The vhost may have dots (`a.b.example.com.<pk>.skynet`)
and combine with routing prefixes; it's purely shape-based.

For this to do anything, the destination visor must actually **forward `:80` to the
vhost server** (`skywire cli serve 80 --preserve-host --proxy-addr 127.0.0.1:8080`);
if `:80` is just the default landing page, the rewritten Host is ignored.
`--preserve-host` is what keeps the rewritten Host intact end-to-end. The rewrite
is skipped on the TLS port when MITM is off (a raw-TLS stream isn't HTTP), but
applies on plain `:80`.

To make those sites **PK-aware** (different content per caller), see
[skynet-website-auth.md](skynet-website-auth.md) — the visor can inject the
authenticated caller's PK as a header the site can trust.

### Your own visor (self-loopback + alias)

Requesting **your own visor's PK** through the proxy
(`http://<self-pk>.dmsg/`) is served **in-process** — the request is handed
straight to the local service instead of dialing out over dmsg/skynet back to
yourself (a wasteful, 202-prone round-trip dmsg doesn't loopback anyway). This
is on by default.

A friendly **alias** makes this convenient: the label `skywire` resolves to the
local visor, so `http://skywire.dmsg/` and `http://skywire.skynet/` both open
the local visor's port-80 landing page through the resolver — no need to type
(or hardcode) the visor's public key.

Both are configurable per resolver (`dmsg_web` / `skynet_web` in the visor
config):

```json5
{
  "self_loopback": true,   // false → take the real self-route (testing)
  "alias": "skywire"       // hostname label for THIS visor; default "skywire"
}
```

The `alias` is just a rename of the local label — set it to `"myhost"` to use
`myhost.dmsg` / `myhost.skynet` instead. It always points at the local visor's
own PK.

`self_loopback: false` is mainly useful to test that your visor is reachable
over its **own** transports — valid for skynet (dmsg won't self-loopback).

## From `curl`

Use `socks5h://` (the `h` makes the **proxy** resolve the hostname — required for
`.dmsg`/`.skynet`):

```
curl -x socks5h://127.0.0.1:4445 http://<visor-pk>.dmsg:80/health
```

### Browsing deployment-service data over dmsg

```
# uptime tracker (dmsg-discovery copy); v3 = with the daily timeline
curl -x socks5h://127.0.0.1:4445 'http://<dmsgd-pk>.dmsg:80/uptimes?v=v3'
# service discovery — list VPN servers
curl -x socks5h://127.0.0.1:4445 'http://<sd-pk>.dmsg:80/api/services?type=vpn'
# transport discovery — all transports
curl -x socks5h://127.0.0.1:4445 'http://<tpd-pk>.dmsg:80/all-transports'
```

Deployment service public keys live in the embedded deployment config; `cli svc`
and the per-command `--*url` flags also surface them.

## From a browser

Set the browser's SOCKS5 proxy to `127.0.0.1:4445` and enable "proxy DNS when
using SOCKS v5" / "remote DNS" so the proxy (not your OS) resolves `.dmsg` /
`.skynet` names. Then open `http://<pk>.dmsg:<port>/` directly. With `--upstream`
configured, ordinary browsing still works through the same proxy entry.

## HTTPS / TLS

A `.dmsg` / `.skynet` stream is already end-to-end encrypted and authenticated by
the target's public key, so plain `http://` through the proxy is secure. To
satisfy a browser's secure-context requirements (or reach a TLS backend), the
resolver can terminate TLS locally with a per-host leaf certificate — see
[../skynet-tls.md](../skynet-tls.md) and `skywire cli resolver ca`.

## Running the resolver on a remote visor

A low-power or poorly-connected device can use a well-connected visor's resolver:
expose that visor's SOCKS5 port over a `cli skynet` port-forward and point your
browser/curl at the forwarded local port. Add `--direct` to the forward for a
reliable single-hop route to the resolver host (it creates the direct transport
on demand and skips the route-finder).

## See also

- [command reference: `skywire cli resolver`](../skywire/cli/resolver/README.md)
- [skynet.md](skynet.md) — `.skynet` port forwarding that skynetweb builds on
- [../skynet-tls.md](../skynet-tls.md) — resolver TLS MITM mode
