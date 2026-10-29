---
name: skywire-route-cli
description: Verified command reference for the `skywire cli route` and `skywire cli rg` command groups, the local-rules vs route-finder-query distinction, route/rule/transport terminology, `route find`/`calc`/`trace` path lookups, rule + route-group inspection, RSN stats, routing-policy preview, and per-flag gotchas. Load when auditing, scripting, or explaining route-plane CLI commands (as opposed to mux/policy internals, that's skywire-routing).
---

# skywire route / rg CLI surface

Two top-level groups under `skywire cli`: **`route`** (view/set local routing
rules + query paths) and **`rg`** (app-centric route-group view). All commands
run against the local visor RPC (`--rpc`, default `localhost:3435`,
env `SKYWIRE_RPC`). Global output flags: `--json`, `--jq <expr>`, `--shape`,
`--via dmsg://<pk>` (proxy the call to a remote visor).

## The one distinction that matters: LOCAL rules vs REMOTE query
- **LOCAL installed rules** (state on THIS visor): bare `route`, `route ls`,
  `route groups`, `rg ls`, `route table-stats`. These read/print what is
  already installed; they never touch the route-finder.
- **REMOTE path lookup** (no local state changed): `route find` (queries the
  route-finder service), `route calc` (computes a path locally from
  transport-discovery data, still no rules installed), `route trace`
  (route-finder path + per-hop dmsg ping).
- **Terminology**: a *transport* is one peer-to-peer link between two visors;
  a *route* is an ordered chain of hops (each hop = one transport) stitched
  into a *rule chain*; a *rule* is the local per-visor state (forward /
  intermediary-forward / consume) that carries traffic for one leg; a *route
  group* is the set of rules carrying one app connection (initiator +
  responder legs, plus any mux transports).

## route, local routing rules
- `route` (bare) or `route ls`, list installed rules (id, type, local/remote
  port, remote-pk, next-route-id, next-transport-id, expire-at). `ls` is a
  convenience alias added for verb-parity with `rg ls`.
- `route --rid <id>`, show one rule; `route --nrid`, print next free route id;
  `route --live` / `-L`, bubbletea watch view (needs a real TTY; fails
  gracefully "could not open a new TTY" in non-interactive shells). `--live`
  cannot combine with `--rid`/`--nrid`.
- `route table-stats`, routing-table counters: `rules` count, `next_id`
  (monotonic route-ID high-water, never reused → watch on a relay to catch
  reserved-ID leaks), `by_type` breakdown. `--json` supported.
- **Mutation (careful on a live visor)**: `route add app|fwd|intfwd`
  (`-i/--rid`, `-j/--nrid`, `-k/--tpid`, `-l/--lpk`, `-m/--lpt`, `-p/--rpk`,
  `-q/--rpt`, persistent `-a/--keep-alive` default 10m) installs a rule;
  `route rm <id>` / `route rm --all` removes. These write real router state —
  don't run against a visor whose route groups are in use. Missing-flag and
  no-arg paths error before any RPC write (safe to probe).

## route find, query the route-finder (REMOTE)
`route find <dst-pk>` (local visor assumed as source) or
`route find <src-pk> <dst-pk>`. Prints `forward:` / `reverse:` hop lists;
`--json` includes per-hop `Latency`.
- Flags: `-n/--min` (default 1), `-x/--max` (default 1000), `-t/--timeout`
  (10s), `-a/--rf` (route-finder addr, default the prod dmsg RF).
- **Back-compat aliases** (hidden): `--addr` == `--rf`; `--min-hops` == `--min`,
  `--max-hops` == `--max`. Both spellings work everywhere across
  find/calc/trace; the short `--min`/`--max` are what help shows.
- `--min 0` is rejected ("Routing disabled. (minhop=0)").
- GOTCHA: the RF intermittently returns `transport not found` for the same
  query that just succeeded, it is route-finder-side flakiness (random BFS
  bound), not a CLI bug. Retry before concluding a flag is broken.

## route calc, compute a path locally (REMOTE data, no RF, no rules)
`route calc <dst-pk>` or `<src> <dst>`. Streams via the visor gRPC BFS by
default; falls back to local compute (offline / `--source dht` / `--by-latency`).
- Flags: `-c/--count` (routes to return, 0=all), `-n/--min` (0 = use visor's
  `routing.min_hops`, fallback 1), `-x/--max` (5), `--source tpd|tps|auto|dht`
  (`tps` asks the setup node for the source and destination own transports and is TPD independent, dht and auto also read from TPD since the DHT store was removed), `--capacity` (report how many disjoint single intermediate routes can exist, which is the warm standby pool ceiling, using no route finder and no TPD), `--queue-cap`, `--by-latency` (rank by cumulative tp
  latency; forces local compute), `-a/--tpd` (transport-discovery addr, NOT
  the route-finder), `-t/--timeout` (30s), fetch-chain flags
  `--no-cxo/--no-rpc/--no-dmsg/--sk/--config`.
- `route calc` with no args prints local-route-calculation enabled/disabled
  status; `--enable`/`--disable` toggle it on the visor (mutates visor state —
  affects how subsequent routes are computed). `--enable --disable` together
  errors.
- `--source <bogus>` is validated up front and rejected on every path (fixed;
  it used to be silently accepted by the streaming path).

## route trace, per-hop latency (route-finder path + dmsg ping)
`route trace <dst-pk>`, walks the forward route to `<dst>` and dmsg-pings each
intermediate, printing a `HOP / PK / RTT / NOTES` table (destination flagged).
- Flags: `-c/--count` (samples/hop, best RTT reported, default 3), `-q/--quiet`
  (suppress the stderr preamble), `-n/--min` (1), `-x/--max` (5), `-t/--timeout`
  (10s), `-a/--rf` (route-finder addr), `--json`, fetch-chain flags.
  `--min-hops`/`--max-hops` hidden aliases as elsewhere.
- Errors on `--count 0` and on tracing the local visor (self).

## route minhops / policy / rsn
- `route minhops <n>`, set `routing.min_hops` on the running visor at runtime
  (NOT persisted to config). `--json` → `{"min_hops":n}`.
- `route policy list | show <name> | test | bench`, routing-policy preview
  (Starlark + WASM presets). `test`/`bench` take `-s/--script` or `-p/--preset`
  (mutually exclusive, one required); `test --dial '{...}'` previews a synthetic
  decision, `bench -n/--iter` times per-call eval. Pure local, no RPC, safe.
  (For applying policies to apps + the measurement rig, see skywire-routing.)
- `route rsn-stats`, embedded route-setup-node request stats; `--reset` clears
  counters. Only works if the visor has `route_setup_sk` configured (else
  "embedded route setup-node not running on this visor"). `--json` supported.
- `route rsn-remote-stats [--pk <rsn-pk>]`, fetch `/stats` from a standalone
  RSN over dmsg (RPC→DmsgHTTP→direct-DMSG chain); defaults to the first
  `route_setup_nodes` entry in the visor config.

## route settings
`route settings` prints every runtime router knob, with defaults, as one catalog. Set knobs as `key=value` arguments, for example `skywire cli route settings mux.sack=false`. `--app <name>` scopes a change to one app, and `--reset` restores the defaults. Take the key names from the printed catalog.

## rg, app-centric route groups
- `rg` (bare) == `rg ls`, table: APP, ROLE (initiator/responder), REMOTE pk,
  PORTS `local:remote`, LATENCY, TX/RX bytes, UP/DOWN speed, ROUTES count,
  MUX yes/no; when ROUTES>1 each mux leg gets a `└─` sub-line (tp id, type,
  latency, fwd rule id).
- Flags: `--filter all|initiator|responder`, `--hops` (append the full forward
  hop path), `-L/--live` (TTY watch; incompatible with `--json`), `--json`.
- `rg ls` is the richer, bandwidth-annotated view of the same groups
  `route groups` shows at the rule level. `rg ls --json` is the ground-truth
  source the mux-route probe parses for route_count.
- NOTE: `rg ls` is the one command in the group that closes its RPC client on
  exit, so it emits a `Closing RPC client connection` DEBUG line on stderr.

## Verified gotchas
- Route-finder `transport not found` is transient (retry).
- `--live` needs a TTY; all three live views degrade gracefully otherwise.
- On a live visor the route-group IDs churn (keepalive re-setup), rule ids you
  saw a moment ago may be gone/renumbered; that is normal, not a leak.
- Hidden aliases: `route find --addr` (==`--rf`), and `--min-hops`/`--max-hops`
  (==`--min`/`--max`) on find/calc/trace all work despite not showing in help.
