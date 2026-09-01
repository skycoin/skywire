# Transports: Querying and Creating

A **transport** is one peer-to-peer link between two visors. Routes
are chains of transports; everything else in the routing layer builds
on them. Types: `stcpr` (TCP), `sudph` (UDP hole-punched), `dmsg`
(relayed — always establishable), `squicr` (QUIC), `webrtc`, and
`stcp` (LAN). A transport is created by one edge dialing the other and
is bidirectional once established.

There are **three distinct sources of transport truth** — keep them
straight when diagnosing:

1. **Local** — the visor's own transport manager. Live and
   authoritative for that visor: `skywire cli tp`.
2. **Remote, live** — another visor's current transports, fetched
   through the Transport Setup Node: `skywire cli tp --remote <pk>`.
3. **Transport Discovery (TPD)** — the network's *registered* record,
   which the route-finder consults: `skywire cli tp disc -p <pk>`.

## Querying

### This visor

```
skywire cli tp                     # live transports: type · id · remote pk · mode
skywire cli tp -t stcpr,sudph      # filter by type
skywire cli tp -p <pk>             # filter by peer
skywire cli tp -b 7                # append bandwidth/latency columns (last 7 days)
skywire cli tp -L                  # live TUI (needs a real terminal)
```

### A remote visor (live, via the Transport Setup Node)

```
skywire cli tp --remote <pk>       # what that visor ACTUALLY has right now
```

Comparing this against `tp disc -p <pk>` answers "does the network's
record agree with reality". Right after a visor restart the gap looks
large — TPD re-publication converges over roughly ten minutes; judge a
chronic mismatch only after that.

### The network (TPD)

```
skywire cli tp disc -p <pk>        # transports TPD registered for a key
skywire cli tp all                 # every transport on the network
skywire cli tp net-stats           # totals by type
skywire cli tp tpd-stats -n 20     # visors ranked by transport count
skywire cli tp metrics -d 7        # per-visor bandwidth metrics
skywire cli tp tree -k <src> -d <dst>   # map the paths between two keys
skywire cli tp tpd-health          # the TPD service itself
```

`tp ls --json` includes measured `throughput_bps` per transport.

## Creating and removing

### From this visor

```
skywire cli tp add <remote-pk> -t stcpr      # dial + establish (PK is positional)
skywire cli tp rm -i <transport-id>          # remove one (ID goes to -i)
skywire cli tp id <pk1> <pk2> -t stcpr       # predict the deterministic UUID
```

Transport IDs are deterministic from (PK pair, type), so re-adding the
same pair+type is idempotent. `stcpr` is the most reliable direct
type; `sudph`/`squicr` depend on the peer's NAT traversal working;
`dmsg` always succeeds (it rides the relay servers).

### On a remote visor (Transport Setup Node)

`skywire cli tps` asks a *target* visor to add/list/remove transports.
The target must trust the requesting visor's transport-setup key
(`transport_setup_nodes` whitelist), else the request is rejected.

```
skywire cli tps list -t <target-pk>                    # the target's transports
skywire cli tps add -t <target-pk> -r <remote-pk> -T stcpr
skywire cli tps rm -t <target-pk> -i <transport-id>
```

The classic use: you cannot dial a NAT-bound peer, so you ask *it* to
dial *you* — `tps add -t <peer> -r <your-pk> -T stcpr` gives you a
direct inbound transport, which collapses routes to that peer down to
one fast hop.

### Autoconnect

```
skywire cli tp auto            # status
skywire cli tp auto on|off     # public autoconnect: how a fresh visor
                               # populates transports without manual tp add
```

## New transports and the route-finder

A new transport feeds the local router immediately, but *other*
visors' route-finders see it only after TPD publication converges
(minutes). If a just-created transport doesn't appear in someone
else's `route find`, wait before debugging.

## Related

- [public-visor.md](public-visor.md) — making a visor reachable so others can dial it
- [privacy-and-performance.md](privacy-and-performance.md) — `no_direct_transports`, `persistent_transports`, transport preference
- [multipath.md](multipath.md) — using several transports/routes at once
