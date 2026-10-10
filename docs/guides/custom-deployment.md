# Running your own deployment

A deployment is the set of services visors use to find each other: dmsg
discovery, a dmsg server, transport and service discovery, the address
resolver, the route finder, a setup node and a transport setup node. Prod
runs one. Any visor can run a whole one of its own, and other visors can
join it instead of prod.

This guide covers running one in a single visor, joining visors to it, the
DNS and TLS a browser visor needs to reach its dmsg server, and the browse
origin.

## Run a deployment in one visor

On a packaged install, set the dmsg server's public address in
`/etc/skywire.conf` and run `skywire autoconfig`:

```
DEPLOYMENT='203.0.113.7:8080'
DEPLOYMENTREDIS='/run/redis/redis-server.sock'
```

`DEPLOYMENT` takes the public host and the dmsg server's port, 8080 when
it is left out. `DEPLOYMENTREDIS` is optional. It takes a redis URL or a
socket path. Without it every entry is kept in memory, so a restart starts
the deployment empty and visors register again within minutes.

Without a package, pass the same values to `config gen`:

```
skywire cli config gen --deployment 203.0.113.7:8080 --deployment-redis redis://127.0.0.1:6379 -o skywire-config.json
```

Either way the visor config gets an `embedded_services` block per service,
and the visor uses this deployment instead of prod. Transport discovery,
service discovery, the address resolver and the route finder run under the
visor's own key, at `dmsg://<visor pk>:80/tpd`, `/sd`, `/ar` and `/rf`. The
dmsg server, dmsg discovery, setup node and transport setup node each get a
key of their own, written into their block.

Ports, from the dmsg server's port P:

| Port | Protocol | Reach | Used by |
|------|----------|-------|---------|
| P | TCP and UDP | public | dmsg server (TCP, ws, QUIC and WebTransport) |
| P+13 | UDP | public | address resolver, for UDP hole punching |
| P+1 | TCP | 127.0.0.1 | dmsg server health |
| P+2 | TCP | 127.0.0.1 | status page of the services, for a reverse proxy |
| P+10 | TCP | 127.0.0.1 | dmsg discovery |

Only P and P+13 need to be open in a firewall or forwarded.

Check it with `skywire cli visor state --select services`. It lists each
service with its address, whether it is running, and its store.

The visor also serves a status page of all its services, a table of them
and the charts of each, at `/status` on its dmsg HTTP port and on
`deployment_status_addr` (P+2 on loopback), which a reverse proxy can put on
a public name.

### Keys and regenerating

A regenerate keeps every block already in the config as it is, with its key
and any edits made to it, and only adds the blocks that are missing. The
keys of the four services that have their own live only in those blocks, so
back up the visor config. A new key for the dmsg server or dmsg discovery
means every joined visor needs the new services-config.

## Join a visor

`autoconfig` writes the deployment's services-config next to the visor
config, as `deployment-services.json`. The same file is printed by:

```
skywire cli config deployment
```

Copy it to the visor that should join, then set in its `/etc/skywire.conf`
and run `skywire autoconfig`:

```
SKYDEPLOY='/opt/skywire/deployment-services.json'
```

`autoconfig` generates the config from that file and adds a systemd drop-in
that sets `SKYDEPLOY` for the visor, so the visor's own fallbacks and its
hourly config refresh use the deployment too, not prod.

Without a package, export `SKYDEPLOY` for both steps:

```
export SKYDEPLOY=/path/to/deployment-services.json
skywire cli config gen -o skywire-config.json
skywire visor -c skywire-config.json
```

The joined visor registers with the deployment's discovery, makes transports
through its dmsg server, and finds routes through its route finder.

## wss names for the dmsg server

A native visor reaches a dmsg server by its IP. A visor running in a browser
cannot open a raw TCP or QUIC connection. It opens its first dmsg session
over `wss://`, which needs a DNS name and a certificate from a public CA,
and switches to WebTransport by IP after that. So a browser visor needs a
DNS name per dmsg server.

The name is derived from the server's key:
`<label>.<suffix>`, where the label is the key in base32, 53 characters,
lower case. Prod uses the suffix `theskywirenetwork.net`, which only applies
to prod's own servers.

To give a deployment's dmsg server a name:

1. Pick a domain you control, say `dmsg.example.net`.
2. Add to the `dmsg-server` block in the visor config's
   `embedded_services`:

   ```json
   "wss_domain_suffix": "dmsg.example.net",
   "ws_tls_address": ":443",
   "ws_tls_cache_dir": "/opt/skywire/dmsg-autocert"
   ```

   With `ws_tls_address` the server gets its own Let's Encrypt certificate
   and serves wss on :443, so nothing else may use :443 on that host. If a
   reverse proxy such as Caddy already owns :443, leave `ws_tls_address`
   out. The server then logs the exact Caddy site block to add, of the form
   `<label>.dmsg.example.net { reverse_proxy 127.0.0.1:P }`.
3. Restart the visor and read the label from its log, on the line starting
   `dmsg-ws: advertising wss`.
4. Add an A record (and AAAA, if the server has IPv6)
   `<label>.dmsg.example.net` pointing at that server's public IP. One
   record per server. Do not point a wildcard at one host, since each server
   terminates its own TLS.
5. Check it with `SKYDEPLOY` set to the deployment's file:
   `skywire cli mdisc check`. It resolves each advertised wss name and
   expects a `426 Upgrade Required` from the server.

The record only matters to browser visors bootstrapping. Native visors keep
connecting by IP if the DNS stops answering.

Once the dmsg server has a wss name, the services-config export carries it as
the server's `address_ws`, so browser visors can join too.

### Join a browser visor

A browser visor reads the same `SKYDEPLOY` setting from the desk's own
`/etc/skywire.conf`. Put the deployment's services-config in the desk's
filesystem, for example at `/opt/skywire/deployment-services.json`, and add:

```
SKYDEPLOY='/opt/skywire/deployment-services.json'
```

then reload the page. The desk runs `skywire autoconfig`, which loads the
deployment before it generates the config and starts the visor. Other
`skywire` commands in the desk follow `SKYDEPLOY` when it is in their
environment, as they do natively.

## The browse origin

The browse origin gives pages a browser visor fetches from the mesh their
own secure origin, isolated from the visor's own page. It serves nothing
but a small static service worker bootstrap. The content itself comes
through the browser visor's own transports, so the origin does not care
which deployment a visor uses.

The services-config export keeps prod's browse origin,
`.haltingstate.net`, so a deployment does not need its own. On a native
visor the local browse proxy uses `.mesh.localhost` by default, which
needs no DNS or certificate.

To serve your own:

1. Use a registered domain separate from the one serving the visor's web
   page, so browsed content stays isolated by origin and cookies.
2. Point a wildcard A record, `*.browse.example.net`, at the host that will
   serve it. A wildcard is right here, because one host serves every name.
3. Get a wildcard certificate by the ACME DNS-01 challenge. With Caddy that
   needs a build with the DNS provider's module, for Cloudflare
   `github.com/caddy-dns/cloudflare`, and an API token limited to DNS edit
   on that one zone:

   ```
   *.browse.example.net {
       tls {
           dns cloudflare {env.CF_API_TOKEN}
       }
       reverse_proxy 127.0.0.1:7998
   }
   ```

4. Serve the bootstrap with the visor web page's server:
   `skywire cli hv serve --browse-origin 127.0.0.1:7998 --browse-suffix .browse.example.net --v-origin https://visor.example.net`.
5. Set `browse_origin_suffix` to `.browse.example.net` in the services-config
   you give to visors, or `BROWSESUFFIX` on a single visor.

## Moving a domain's DNS to an API-driven host

The wildcard certificate above, and anything else that edits records
automatically, needs a DNS host with an API. Moving a domain there without
losing a record:

1. List every record at the current DNS host. A zone cannot be listed from
   outside, so read it from the host's own records page.
2. Write them as a BIND zone file and import it at the new host with any
   proxying off. A proxy in front of a dmsg server breaks wss.
3. Compare the imported records with the list, and query the new
   nameservers directly to check they answer the same as the old ones.
4. Change the nameservers at the registrar. Some registrars, 007names among
   them, put their own back unless their records and old nameserver entries
   are deleted first.
5. Check through public resolvers that every name still resolves. The old
   nameservers keep answering until resolvers drop their cached delegation,
   which can take hours.

## Related

- [config-gen.md](config-gen.md) — every variable `autoconfig` reads
- [dmsg-tools.md](dmsg-tools.md) — running deployment services inside a visor, and probing dmsg servers
- [deployment-health.md](deployment-health.md) — health, profiles and logs of the services over dmsg
- [../deployment/DEPLOYMENT_SYSTEMD.md](../deployment/DEPLOYMENT_SYSTEMD.md) — running the services as separate units
