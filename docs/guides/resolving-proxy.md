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

- `<public-key>` — the target visor or service: its 66-character hex key, or
  the same key as a 53-character base32 label (`skywire cli pk dnslabel <hex>`).
  Use the base32 form with SkyDNS, since a DNS label is at most 63 characters,
  and for HTTPS in a browser, which refuses a certificate for the hex name.
- `:<port>` — the **dmsg port** the target serves HTTP on. **Deployment services
  serve their API on dmsg port `80`.**
- `.dmsg` addresses may carry routing prefixes, e.g. `<server-pk>.<dest-pk>.dmsg`
  pins a specific dmsg rendezvous server. Use `cli tp route-addr` to build one.

### Named sites / virtual hosts

A label to the **left** of the destination PK that is *not* itself PK- or
TpID-shaped becomes a **vhost** — the resolver rewrites the outgoing HTTP `Host:`
header to it. So:

```
http://example.com.<base32-pk>.skynet/
```

dials `<visor-pk>`'s port 80 and sends `Host: example.com`. If that visor forwards
its port 80 to a reverse proxy (Caddy/nginx/traefik) that dispatches by Host, the
proxy serves the `example.com` vhost — letting **one visor expose many real-domain
sites** over skynet (or dmsg). The vhost may have dots (`a.b.example.com.<pk>.skynet`)
and combine with routing prefixes; it's purely shape-based.

For this to do anything, the destination visor must actually **forward `:80` to the
vhost server** (`skywire cli serve add 80 --to 127.0.0.1:8080 --preserve-host`);
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

### Service aliases (configured deployment services)

Beyond `skywire` (your own visor), the resolver also exposes **short aliases for
the deployment services this visor is configured to use**, so you can reach a
service's HTTP API by name instead of its 66-char public key. They're
**auto-derived from the visor config** — nothing to maintain — and resolve on the
**`.dmsg`** suffix:

| alias | service |
|-------|---------|
| `dmsgd` | dmsg discovery |
| `tpd` | transport discovery |
| `ar` | address resolver |
| `rf` | route finder |
| `sd` | service discovery |
| `rewards` | reward system |
| `rsn`, `rsn0`, `rsn1`, … | route setup nodes (bare label = the first) |
| `tsn`, `tsn0`, `tsn1`, … | transport setup nodes (bare label = the first) |
| `dmsg0`, `dmsg1`, … | dmsg servers (the **live** discovery list, sorted by PK) |

So `http://tpd.dmsg/health` reaches the transport discovery, `http://rsn.dmsg/health`
the first route setup node, and `http://dmsg0.dmsg/health` the first dmsg server.

Notes:

- A service is aliased only when it has a resolvable `dmsg://` URL in the config;
  a plain-HTTP-only (or unset) service is silently omitted.
- The `dmsg*` servers aren't registered as discovery *clients*, so they're reached
  via the visor's **direct** dmsg client (self-rendezvous); everything else goes
  over the normal discovery dial. The `dmsg*` set tracks the **live** discovery
  list, not the static bootstrap config.
- `skywire` (your own visor) wins on any name collision.
- An unknown label still **fails closed** — it's neither a PK nor a known alias,
  so it resolves to nothing rather than somewhere wrong.

### Directory page (`home.dmsg`)

`http://home.dmsg/` is a **directory of every alias above**, rendered by the
resolver itself so you don't have to remember the names. Open it in a browser
(with the SOCKS5 proxy set) and click through to any service.

It is **synthetic and proxy-only**: the resolver generates the page in-process
and serves it straight back over the SOCKS5 connection — nothing is dialed out,
and the visor does **not** serve it on any real port. It exists only *as seen
through the proxy*. The service links point at each service's `/health` endpoint
(deployment services don't serve `/`), while the `skywire` link opens your
visor's real landing page at `/`. `home` is a reserved label (it shadows any
same-named configured alias).

### Status pages (`status.dmsg`, `status.skynet`)

Each resolver serves a local status page in-process — no dial-out:
`http://status.dmsg/` (from dmsgweb) and `http://status.skynet/` (from
skynetweb) show the proxy's logs and live per-leg mux view. The skysocks
client serves the analogous `http://status.skysocks/` on its own SOCKS5
listener (`:1080`). Like `home.dmsg`, these exist only as seen through
the proxy. Details: [proxy status pages &
interstitial](../proxy-status-and-interstitial.md).

## From `curl`

Use `socks5h://` (the `h` makes the **proxy** resolve the hostname — required for
`.dmsg`/`.skynet`):

```
curl -x socks5h://127.0.0.1:4445 http://<visor-pk>.dmsg:80/health
```

### Browsing deployment-service data over dmsg

Using the [service aliases](#service-aliases-configured-deployment-services)
(or the raw `<service-pk>.dmsg` if you prefer):

```
# uptime data (served by dmsg discovery); v3 = with the daily timeline
curl -x socks5h://127.0.0.1:4445 'http://dmsgd.dmsg/uptimes?v=v3'
# service discovery — list VPN servers
curl -x socks5h://127.0.0.1:4445 'http://sd.dmsg/api/services?type=vpn'
# transport discovery — all transports
curl -x socks5h://127.0.0.1:4445 'http://tpd.dmsg/all-transports'
# reward system
curl -x socks5h://127.0.0.1:4445 'http://rewards.dmsg/'
```

The aliases are auto-derived from the visor config; the raw public keys also live
in the embedded deployment config, which `cli svc` and the per-command `--*url`
flags surface.

## One port for everything: skysocks-client on `:1080`

The skysocks client hands `.dmsg` and `.skynet` names to the visor's
resolvers in-process and sends everything else out through its exit. A
program that already uses `127.0.0.1:1080` reaches the mesh with no other
setting. It picks up a resolver that comes up later within a minute.

```
curl -x socks5h://127.0.0.1:1080 http://<visor-pk>.skynet/health
```

Use `:4445` when you want mesh names without a skysocks exit.

## Where other traffic goes

A name that is not `.dmsg` or `.skynet` goes to the resolver's upstream:

| `DMSGWEBUPSTREAM` / `SKYNETWEBUPSTREAM` | other names |
|---|---|
| empty (default) | skysocks-client on `:1080` when it autostarts, else direct |
| `127.0.0.1:1081` | that SOCKS5 proxy |
| `direct` | dialed from this computer, no proxy |
| `none` | refused |

dmsgweb's upstream is skynetweb, so the setting that matters is
`SKYNETWEBUPSTREAM`. Set it in `skywire.conf` and run `skywire autoconfig`.

Per-domain rules override it. They apply live and survive a regenerate:

```
skywire cli resolver route add example.com 127.0.0.1:1081
skywire cli resolver route add lan.example direct
skywire cli resolver route add tracker.example none
skywire cli resolver route          # list
```

## From a browser

**Firefox, Waterfox, LibreWolf.** Settings, Network Settings, Manual proxy.
Set SOCKS Host `127.0.0.1`, port `4445` (or `1080`), SOCKS v5, and tick
"Proxy DNS when using SOCKS v5". Then open `http://<base32-pk>.dmsg/` directly.

**Chromium, Brave, Chrome.** These have no proxy setting of their own. Either
use [SkyDNS](#skydns-no-proxy-at-all) and no proxy, or start the browser with
`--proxy-server="socks5://127.0.0.1:4445"`. For Brave that flag can go in
`~/.config/brave-flags.conf`, one flag per line.

## HTTPS

A `.dmsg` / `.skynet` stream is already encrypted end to end and
authenticated by the target's key, so `http://` through the proxy is secure.
A browser still wants `https://` for some features, and some sites redirect
to it. The resolver can terminate HTTPS locally with a certificate it mints
per name from a CA on this computer. The CA is name constrained to `*.dmsg`
and `*.skynet`, so it cannot vouch for any other site.

1. Create the CA.

   ```
   skywire cli resolver ca gen
   skywire cli resolver ca path          # where it went
   skywire cli resolver ca fingerprint   # to check a browser's copy
   ```

2. Point both resolvers at it in `skywire.conf` and regenerate.

   ```
   RESOLVERCA='/home/<user>/.skywire/resolver'
   ```

   ```
   skywire autoconfig
   ```

3. Trust it in each browser. `skywire cli resolver ca install` adds it to
   the OS store, which Chromium-based browsers read through
   `~/.pki/nssdb`. Firefox keeps a store per profile:

   ```
   certutil -A -n "Skynet Resolver Local CA" -t "C,," -i ~/.skywire/resolver/ca.crt -d sql:<profile-dir>
   ```

The decrypted request goes to the target's port 80, so a site published on
port 80 is reachable over `https://` too.

Use the base32 form of the key for HTTPS in a browser. Chromium-based browsers
refuse a certificate for the 66-character hex name, though curl accepts it.

```
skywire cli pk dnslabel <hex-pk>
https://<base32-label>.skynet/
```

## SkyDNS: no proxy at all

The `skydns` app makes `.dmsg` and `.skynet` names work for every program on
the computer, with no proxy setting. It answers DNS for the two zones with
addresses in `198.18.0.0/15`, routes that range to a tun, and bridges each
connection to the named peer through the visor's resolver. Routes are reused
across connections and a slow first dial gets the resolver's interstitial page.

Only base32 names work, since a DNS label cannot hold a hex key. Aliases such
as `rewards.dmsg`, `home.dmsg` and `skywire.dmsg` are answered by the proxy and
do not resolve through SkyDNS.

Setup on a Linux desktop:

1. Where systemd-resolved runs, SkyDNS hands it only the `dmsg` and `skynet`
   zones, and a config made as root (as the packages make it) turns SkyDNS on
   by default. Without systemd-resolved, SkyDNS puts itself first in
   `/etc/resolv.conf` and passes every other name to the nameserver that was
   first, so it stays off until you set `SKYDNS=true`. To use systemd-resolved
   instead (with NetworkManager, also set `dns=systemd-resolved` in its
   `[main]` section):

   ```
   sudo systemctl enable --now systemd-resolved
   sudo ln -sf /run/systemd/resolve/stub-resolv.conf /etc/resolv.conf
   ```

2. To set it either way, use `skywire.conf`, with the CA from [HTTPS](#https)
   if you want `https://`, and regenerate.

   ```
   SKYDNS=true
   RESOLVERCA='/home/<user>/.skywire/resolver'
   ```

3. The tun needs `CAP_NET_ADMIN`. A visor running as root has it. Otherwise
   the first start asks for a password once, through pkexec, and creates a
   persistent `skydns0` tun owned by your user. Later starts use it without
   asking, until the next reboot. See [permissions](permissions.md#skydns).

Check it:

```
resolvectl status skydns0
curl http://<base32-label>.skynet/health
```

The Android app runs the same thing on the phone's VPN slot.

## Running the resolver on a remote visor

A low-power or poorly-connected device can use a well-connected visor's resolver:
expose that visor's SOCKS5 port over a `cli skynet` port-forward and point your
browser/curl at the forwarded local port. Add `--direct` to the forward for a
reliable single-hop route to the resolver host (it creates the direct transport
on demand and skips the route-finder).

## See also

- [command reference: `skywire cli resolver`](../skywire/cli/resolver/README.md)
- [skynet.md](skynet.md) — `.skynet` port forwarding that skynetweb builds on
- [../skynet-tls.md](../skynet-tls.md) — how the resolver terminates TLS
- [permissions.md](permissions.md#skydns) — what SkyDNS needs to create its tun
