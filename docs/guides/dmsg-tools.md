# DMSG Tools and Carriers

DMSG is skywire's relay network: always-on servers relay end-to-end
encrypted streams between clients addressed by public key. It is what
makes a NAT-bound visor reachable with zero configuration, and it
carries the deployment services' HTTP APIs (`dmsg://<pk>:80/...`).

Two command trees exist — worth keeping straight:

- **`skywire dmsg …`** — standalone tools and servers: `curl`, `conf`,
  `self-ping`, `server`, `socks`, `web`, `disc`, `http`, `ip`. These
  bootstrap their own dmsg client and need no visor.
- **`skywire cli dmsg …`** — the *visor's* dmsg client over RPC
  (`sessions`, `converge`, `port-hits`, diagnostics) plus standalone
  utilities hoisted in (`curl`, `cat`, `scp`, `iperf`, `probe`,
  `chat`).

## Carriers: how a client reaches a dmsg server

A dmsg server can be dialed over four **carriers**: raw **tcp**,
**quic**, **ws/wss** (WebSocket — what a browser uses), and **wt**
(WebTransport over HTTP/3). Above the carrier, every session is the
same Noise-encrypted, yamux-multiplexed pipe — the carrier only
determines how bytes reach the relay.

The native default is QUIC when the server advertises it, else TCP.
On restrictive networks (only 443/HTTPS egress), force the
browser-style carriers:

```
# visor config, dmsg section:
#   "carriers": ["wt", "ws"]
```

Servers advertise their carriers in the discovery entry: `address`
(tcp), `address_udp` + `protocol: quic`, `address_ws` (a TLS-fronted
`wss://` hostname), and `address_wt`.

## Checking server reachability

Three probes at increasing depth:

```
skywire cli mdisc servers        # servers by load, from the discovery
skywire cli mdisc check          # DNS/TLS/upgrade check of every advertised wss front
skywire dmsg conf probe          # REAL sessions: every server × every carrier
```

`dmsg conf probe` establishes a full Noise session per advertised
carrier of every server and prints a matrix (`ok <latency>` / `FAIL
<reason>`). Narrow it with `--server <pk>` or `--carrier ws,quic`;
`--json` for scripts. This validates exactly what a browser-based
visor dials for `wss://`, not just a TCP connect.

Two more targeted checks:

```
# dial your own key back through ONE specific server
skywire dmsg self-ping --server <server-pk>@<ip:port>

# reach a service THROUGH one specific server
skywire dmsg curl -S <server-pk>@<ip:port> dmsg://<service-pk>:80/health
```

Per-carrier probe of a single server (with the visor's tooling):

```
skywire cli svc health --dmsg-server <server-pk> --carriers wt,ws,quic,tcp
```

## The visor's dmsg client

```
skywire cli dmsg sessions        # which servers each internal client is on
skywire cli dmsg port-hits       # inbound streams that found no listener (err 306)
skywire cli dmsg converge -c wt,ws   # re-dial sessions onto preferred carriers
```

`converge`, `connect-all`, and the `diag reconnect`/`porter-reset`
commands tear down live dmsg streams — don't run them on a visor doing
real work.

## Standalone utilities

```
skywire dmsg curl dmsg://<pk>:80/health          # HTTP over dmsg
skywire cli dmsg cat <pk>:<port>                 # netcat over the mesh
skywire cli dmsg scp <pk>:/remote/file ./local   # file copy (dmsgscp host, port 23)
skywire cli dmsg iperf <pk>:<port> -d 10s        # throughput measurement
skywire cli dmsg probe <pk> --ports 22,80,136    # listener reachability sweep
```

`dmsg probe --server <pk>` forces the probe through one relay;
`--standalone`/`--sk` run without a local visor. Useful dmsg ports:
**80** dmsg-HTTP, **22** pty, **23** scp, **136** route setup.

## Run a dmsg server inside the visor

A host that runs a public dmsg server as a separate unit
(`skywire dmsg server start /etc/skywire-dmsg.json`) can fold it into the
visor process and save the second Go runtime. Stop and disable that unit,
drop it from `RESTART_SERVICES`, then set in `/etc/skywire.conf`:

```
DMSGSERVERCONF='/etc/skywire-dmsg.json'
```

and run `skywire autoconfig`. The server keeps its own key, ports, wss domain
and health endpoint from that file; it appears in `skywire cli mdisc servers`
as before. In the visor config this is `dmsg.server.config_path`.

## Run deployment services inside the visor

transport-discovery, address-resolver, route-finder and service-discovery
can run in the visor process instead of as their own containers. Each is
served under a path prefix on the visor's dmsg HTTP port, so it is
addressed by the visor's key: `dmsg://<visor pk>:80/tpd/...`,
`/ar/...`, `/rf/...`, `/sd/...`. The service keeps no key and no
listener of its own; its CXO aggregators and publishers run on the
visor's dmsg client under the visor's key. Requests over dmsg carry the
caller's key, so the services' whitelists behave as before.

In the visor config, `embedded_services` takes the same blocks as a
`services.json` for `skywire svc run`, plus an optional `prefix`:

```json
"embedded_services": [
  { "type": "transport-discovery", "redis": "redis://127.0.0.1:6379", "entry_timeout": "5m" },
  { "type": "route-finder", "redis": "redis://127.0.0.1:6379" },
  { "type": "address-resolver", "redis": "redis://127.0.0.1:6379", "udp_addr": ":30178" }
]
```

`/tpd/health` and the other services' `/health` stay where they were,
under their prefix; the visor's own `/health` is unchanged. Clients take
the prefixed address in place of the service's old `dmsg://<service pk>:80`.
A block's `addr` is honored as well: the same handler, without the
prefix, on that plain-HTTP address, for callers that still reach the
service the old way. The visor's own clients reach a service it hosts
in-process, so its config may point at its own key.

Registration and telemetry reach the services over CXO. Visors publish
each feed on a fixed dmsg port, and the service aggregates it on the
same port. The visor publishes on those ports too, under the same key,
so an embedded service's aggregator runs on the visor's own publisher
node for that port. The visor hands the service its own feed in-process.
A publisher on such a port keeps its store in memory.

`skywire cli visor state --select services` lists each embedded service:
its address, whether it is running, its store and nonce store, and each
CXO aggregator and publisher. For an aggregator it shows the port,
whether it shares the visor's node, and its connection and subscription
counts.

The e2e suite runs this shape: `visor-s` in `docker/docker-compose.yml`
hosts the four services (`docker/integration/visorS.json`), and every
other visor's config addresses them by its key.

## Keeping the embedded server list fresh (maintainers)

The binary embeds a snapshot of the deployment's dmsg servers
(`deployment/services-config.json`) so a fresh client can bootstrap
before it can query the discovery. Refresh it from the live discovery:

```
skywire dmsg conf pull
go generate ./deployment/
```

The pull preserves each server's full advertised endpoint set —
including the `wss://` addresses a browser-based visor needs at boot.

## Related

- [resolving-proxy.md](resolving-proxy.md) — `.dmsg` hostnames from a browser
- [deployment-health.md](deployment-health.md) — service health/pprof/logs over dmsg
- [standalone-dmsgpty-host.md](standalone-dmsgpty-host.md) — pty without a visor
