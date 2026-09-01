# Deployment Services: Health, pprof, and Logs over DMSG

The deployment services — transport discovery (TPD), dmsg discovery,
service discovery (SD), route finder (RF), address resolver (AR),
config bootstrap, uptime tracker — are reachable **over dmsg only**:
each serves its HTTP API on dmsg port 80 under its own public key.
Everything on this page therefore works from anywhere a dmsg client
works, with no clearnet endpoint involved.

## The fast first look

```
skywire cli svc health
```

One table: every service and dmsg server with status, latency,
version, and public key. This is also how you *get* the current
service keys — always pull them fresh rather than hardcoding.

Raw health of one service:

```
skywire dmsg curl dmsg://<service-pk>:80/health
# → {service_name, build_info{version,commit,date}, started_at, ...}
```

### Via a specific dmsg server

Reachability questions are often really *relay* questions — "is the
service down, or can I just not reach it through the relay I picked?"
Pin the path:

```
skywire cli svc health --service <service-pk> --dmsg-server <server-pk>
skywire dmsg curl -S <server-pk>@<ip:port> dmsg://<service-pk>:80/health
```

Loop the second form over the server list to map exactly which relays
can carry you to a service. A transient
`dmsg error 202 - cannot connect to delegated server` on *unpinned*
dials usually means the relay guess was unlucky — retry, or pin.

## Service read APIs

Structured queries against the discovery services, over the same
dmsg-only chain (CXO cache → visor RPC → direct dmsg):

```
skywire cli svc tpd stats              # network transport totals by type
skywire cli svc tpd per-key-stats      # every visor's transport counts
skywire cli svc tpd bandwidth -p <pk>  # per-visor bandwidth
skywire cli svc dmsgd all-servers      # dmsg servers registered in the discovery
skywire cli svc dmsgd clients -p <server-pk>
skywire cli svc ar check <pk>          # is a visor AR-registered (without its IP)
skywire cli ut                         # uptime tracker: per-day % per visor
skywire cli sd                         # joined SD + TPD + UT network table (slow)
```

All honor `--json`; `--direct` skips the local visor and uses a
CLI-owned client.

## Debug surfaces: pprof and logs

Services and visors expose their debug endpoints on the same dmsg:80
listener, gated by a **survey whitelist** — an allowlist of keys in
the deployment's service configuration. Ungated paths (`/health`) work
for everyone; `/debug/*` returns 401 unless the *fetching client's*
key is whitelisted. The fetch rides a resolving SOCKS5 proxy (see
[resolving-proxy.md](resolving-proxy.md)) whose dmsg client runs under
a whitelisted key — a standalone one can be started with
`skywire dmsg web --sk <whitelisted-sk>`. Below, `$PROXY` is that
proxy's `host:port`:

```
# pprof — full suite: heap, goroutine, profile, trace, allocs, block, mutex
curl -x socks5h://$PROXY --max-time 30 http://<service-pk>.dmsg/debug/pprof/heap -o heap.pb.gz
go tool pprof -http=:0 heap.pb.gz

# goroutine count at a glance (leak watching)
curl -x socks5h://$PROXY "http://<service-pk>.dmsg/debug/pprof/goroutine?debug=1" | head -1

# a service's recent log ring
curl -x socks5h://$PROXY http://<service-pk>.dmsg/debug/log | tail -40
```

Give dmsg fetches time (`--max-time 25` or more): route setup plus
relay can take seconds, and a short timeout reads as an empty `000`
response — a transient, not an absence. Retry before concluding an
endpoint is missing.

The CLI wraps the common visor-side fetches:

```
skywire cli log info <pk>              # /node-info
skywire cli log pprof <pk> heap        # any pprof profile, ready for go tool pprof
skywire cli log file <pk> --follow     # the visor's log file, server-side filtered
```

### Visors vs services — different debug paths

A **visor's** dmsg:80 surface serves `/health`, `/node-info`,
`/debug/pprof/*`, `/stats/transports` (live per-transport
sent/received/throughput — gold when tracing a dead route hop),
`/stats/uptime`, `/stats/services`, and its recent log at
`/skywire.log`. It does **not** serve `/debug/log` — that path exists
only on deployment services. For a visor's live log ring the RPC route
is usually better anyway:

```
skywire cli visor log --follow --min-level debug --via dmsg://<pk>
```

## Related

- [resolving-proxy.md](resolving-proxy.md) — the SOCKS5 proxies that resolve `.dmsg` hostnames
- [dmsg-tools.md](dmsg-tools.md) — carrier probing, per-server pinning, dmsg utilities
- [remote-visor-cli.md](remote-visor-cli.md) — full CLI control of a remote visor
