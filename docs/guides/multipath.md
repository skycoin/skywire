# Multipath Routes and Routing Policies

Skywire apps can carry one connection over **several parallel routes**
at once — a multiplexed ("mux") route group. Packets stripe across the
legs; a dead leg is dropped and replaced without resetting the
connection. This page covers operating and observing mux routes and
the routing-policy engine that manages them. For *why* you would (the
privacy and resilience rationale), see
[privacy-and-performance.md](privacy-and-performance.md).

## Vocabulary

- A **route group** (RG) is the set of routes carrying one app
  connection. `skywire cli rg ls` shows them app-centrically: remote,
  ports, latency, bytes, and one `└─` line per mux leg.
- A **leg** is one route inside a mux group — its own chain of
  transports with its own latency and health.
- `skywire cli route ls` shows the underlying routing *rules*;
  `route find` queries the route-finder service; `route calc` computes
  paths locally from discovery data; `route trace <pk>` walks a path
  and pings each hop.

## Starting an app with mux

```
skywire cli proxy start --pk <server-pk> --mux 4
skywire cli vpn start   --pk <server-pk> --mux 4
skywire cli skynet start --pk <srv-pk> -r 8080 -l 9090 --routes 4
```

`--mux` and `--routes` are the same knob (each accepts the other as an
alias). `--min-hops N` forces every leg through at least N
intermediaries. A visor-global default can be set in the config
(`routing.mux_routes`, `routing.min_hops`); an explicit per-app value
overrides it.

## Observing a live route group

`skywire cli proxy mux info` is the per-leg telemetry surface for a
running proxy session:

```
skywire cli proxy mux info --name skysocks-client            # one snapshot
skywire cli proxy mux info --name skysocks-client --watch 1s # live
skywire cli proxy mux info --ndjson run.ndjson --duration 60s
```

Each sample carries, per leg: route index, intermediate key, transport
kind, instantaneous send/receive rate, RTT, retransmit count, and gate
state (`active`/`standby`) — plus lifecycle events
(`established`/`promoted`/`demoted`/`dropped`) on the same clock. The
NDJSON stream is the raw material for any throughput or stability
analysis.

## Reshaping a live route group

```
skywire cli route calc <server-pk> --count 4 --json > legs.json
skywire cli proxy mux set --legs legs.json --name skysocks-client
skywire cli proxy mux add --route r.json
skywire cli proxy mux rm <transport-id>
skywire cli proxy mux auto fastest        # built-in latency control loop
```

`mux set` reconciles the running group to a target leg set (add-only
unless `--prune`). These mutate a live session — expect a brief
perturbation.

## Controlled bandwidth measurement: `mux-bw`

`skywire cli visor ping mux-bw` answers "does mux actually help
between these two visors" without any external server: it plans its
own disjoint routes, drives load in-process, and reports per-leg and
aggregate rates:

```
skywire cli visor ping mux-bw <pk> --routes 3 --min-hops 2 --duration 30s
skywire cli visor ping mux-bw <pk> --routes 3 --output run.ndjson --probe-rtt
```

## Routing policies

A routing policy decides, per dial and per tick, how many legs to run
and which peers to use. Policies are sandboxed WASM or Starlark
programs; several presets ship in the binary:

```
skywire cli route policy list          # rotating-bw, elastic-mux, latency-adaptive, …
skywire cli route policy show rotating-bw
skywire cli route policy test -p rotating-bw --dial '{...}'   # preview offline
skywire cli route policy bench -p rotating-bw                 # per-call cost
```

Apply one:

```
# at app start
skywire cli proxy start --pk <server-pk> --routing-policy preset:rotating-bw

# at runtime, no restart
skywire cli visor app arg routing-policy skysocks-client preset:latency-adaptive

# in the config, for every dial
#   "routing": { "policy_per_dial": "preset:adaptive" }
```

Custom policies load from a file: `--routing-policy @my-policy.star`
or `@my-policy.wasm`. See the
[routing-policy RFC](../routing_policy_rfc.md) for the ABI.

## Reading route-setup behavior from the log

`skywire cli visor log` (add `--via dmsg://<pk>` for a remote visor)
tags route-setup events usefully:

- `Found routes` / `Requesting new routes` — route-finder interaction.
- `Direct transport ready; using 1-hop route` — the fast path when a
  direct transport exists.
- `dmsg error 306 - no associated listener` — the far end has no
  listener on the dialed routing port (wrong port, or the app is not
  running there).

## Related

- [manual-routing.md](manual-routing.md) — hand-building routing rules
- [transports.md](transports.md) — the links the legs are built from
- [privacy-and-performance.md](privacy-and-performance.md) — mux/min-hops as privacy levers
