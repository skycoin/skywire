---
name: skywire-visor-state
description: One call runtime introspection of a visor with `skywire cli visor state`, the runtime counterpart of `config show`. A curated snapshot without secrets, with --select, --watch, --jq, --shape and --via. Load for a fast answer about a visor's health, routing, transports, apps, policy, embedded services or which subsystems are wired.
---

# skywire cli visor state

`skywire cli visor state` returns a curated snapshot of the visor's live runtime. It never includes secret keys. It is the runtime counterpart of `config show`, which prints the config on disk.

## Workflow
1. `skywire cli visor state --shape` prints the schema skeleton with every field. It needs no live data, so read it first to learn the JSON paths.
2. `skywire cli visor state --jq '<expr>'` projects what you want. `--jq` implies `--json`.
3. `--via dmsg://<pk>` runs it against a remote visor. The remote must allow the caller, as for other debug surfaces.
4. `--select a,b` builds only those subtrees on the server. The full snapshot is large, so select when you can. Keys are `summary`, `health`, `routing`, `mux`, `pool`, `apps`, `transports`, `modules`, `cxo`, `services`, `proxy`, `diag`, `roles`, `runtime`, `network`.
5. `--watch 1s` streams one NDJSON snapshot per tick. It composes with `--select`, `--jq` and `--via`.

## Top level keys
`at`, `suspended`, `summary`, `health`, `service_health`, `routing_stats`, `route_groups`, `mux_route_groups`, `mux_counters`, `pool`, `routing_policy`, `router_config`, `apps`, `transports`, `persistent_transports`, `modules`, `cxo`, `services`, `proxy`, `roles`, `diag`, `runtime`, `network`, `tpd_leaf_publisher`, `notes`. The `--shape` output is the authority.
- `summary` embeds the `visor info` overview.
- `modules` shows which optional subsystems are wired, often the answer to "why is there no stats feed".
- `services` lists each embedded deployment service with its running state, whether it has its own key, restarts, and its CXO aggregators and publishers.
- `roles` shows what the visor is for the network, such as its dmsg server and relay roles.
- `network` is the public IP (`public_ip_source` dmsg or stun, `public_ip_at`), the NAT class, `local_addresses` after the virtual interface filter and the network policy (`allow`, `deny`, and `policy_excluded` for what it removed), `advertised` per transport type, and the last move (`moves`, `last_move_reason`, `last_move_change`). `skywire cli visor network show` prints it as text.
- `notes` holds per section errors. A suspended or half started visor still returns a useful snapshot.

## Common queries
```
skywire cli visor state --select health --jq '.health'
skywire cli visor state --select summary --jq '.summary.overview.build_info.version'
skywire cli visor state --select transports --jq '.transports | group_by(.type) | map({(.[0].type): length}) | add'
skywire cli visor state --select services --jq '.services[] | {name, running, own_key}'
skywire cli visor state --select mux --jq '.mux_route_groups'
skywire cli visor state --select modules --jq '.modules'
skywire cli visor state --via dmsg://<pk> --jq '.routing_policy'
```
To compare a field across a fleet, loop over the keys from `skywire cli visor hv ls` with `--via`, one at a time.

## Changing state
`visor state` is read only. Live setters are separate commands such as `route settings`, `route minhops`, `proxy settings`, `visor app arg ...`, `tp auto` and `visor skyenv`. See skywire-route-cli and skywire-visor-cli. A config edit on disk needs a restart or regen to take effect.

## GC and memory (runtime)
`--select runtime` reports the collector and memory figures read from Go's `runtime/metrics`, so it reads the same under Go and TinyGo. A metric the runtime does not provide is left out of the output, not zero.
```
skywire cli visor state --select runtime --jq '.runtime | {compiler, heap_live_bytes, heap_goal_bytes, live_limit_ratio, rss_bytes, last_1m, last_5m}'
```
`last_1m` and `last_5m` hold `pause_share` (stop the world CPU time over total CPU time), `gc_share`, `collections_per_min` and `covered_s`. `last_5m.pauses` and `pauses_total` give the p50, p99 and max pause, rounded up to the histogram bucket. A sampler takes one reading every 5 s into a ring (visor config `gc_trace_samples`, default 360).

The same ring is served as `/debug/gctrace` on the localhost pprof listener and on the survey whitelisted dmsg :80 surface. It is plain text, one line per sample with the deltas since the last one, or JSON with `?json=1`.
```
curl -x socks5h://127.0.0.1:4443 http://<pk>.dmsg/debug/gctrace
```
